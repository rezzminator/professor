package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// layoutStagedPrompts plants a staged prompt directory: layout work (a host
// still migrating) that moves no database.
func layoutStagedPrompts(t *testing.T, env LayoutEnv) string {
	t.Helper()
	dir := filepath.Join(env.ManagedRoot, "harness-prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestLayoutApplyRefusesANameSyncJobStillRunningAfterTheStop proves a job the
// schedule started before the stop refuses the apply before its first write,
// with exit 97 and the stop command, the stopped scheduler held for its
// restart.
func TestLayoutApplyRefusesANameSyncJobStillRunningAfterTheStop(t *testing.T) {
	env := layoutFixture(t)
	staged := layoutStagedPrompts(t, env)
	runner := &layoutTestRunner{
		states:   map[string]string{nameSyncServiceUnit: "activating"},
		mainPIDs: map[string]int{launchdLabel: 4242},
	}
	env.runner = runner
	journal := NewJournal(context.Background(), env)
	dir, err := ApplyLayout(context.Background(), env, journal, true, &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "refused before any change:") ||
		!strings.Contains(err.Error(), SchedulerRefusal("install", err)) || LayoutExitCode(err) != 97 || dir != "" {
		t.Fatalf("running job after the stop: dir=%q exit=%d err=%v", dir, LayoutExitCode(err), err)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged prompts removed before the refusal: %v", err)
	}
	if journals, err := InstallJournals(env.Home); err != nil || len(journals) != 0 {
		t.Fatalf("journal written before the refusal: %+v err=%v", journals, err)
	}
	if !slices.Equal(journal.deferredScheduler, layoutSchedulerServices()) {
		t.Fatalf("deferred %q, want the stopped scheduler held: %q", journal.deferredScheduler, runner.calls)
	}
	journal.RestartSchedulerUnits(1, io.Discard)
}

// TestLayoutManagedCleanupAloneTouchesNoUnit proves the advisory
// managed-cleanup row is never layout work: a host whose only non-ok row it is
// probes, stops and restarts no unit, in the apply and in its preview.
func TestLayoutManagedCleanupAloneTouchesNoUnit(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(filepath.Join(env.ManagedDir, "pfm.json")); err != nil {
		t.Fatal(err)
	}
	runner := &layoutTestRunner{}
	env.runner = runner
	findings := ClassifyLayout(env)
	if finding := layoutFindingByPath(
		findings,
		layoutRowManagedCleanup,
		filepath.Join(env.ManagedDir, "pfm.json"),
	); finding.Verdict != VerdictCreate {
		t.Fatalf("managed-cleanup = %+v, want create", finding)
	}
	runner.calls = nil
	if err := PreviewLayoutStop(context.Background(), env, findings); err != nil || len(runner.calls) != 0 {
		t.Fatalf("preview err %v calls %q, want no probe", err, runner.calls)
	}
	journal := NewJournal(context.Background(), env)
	if _, err := ApplyLayout(context.Background(), env, journal, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "ActiveState") || strings.HasPrefix(call, "launchctl") ||
			strings.Contains(call, " stop ") || strings.Contains(call, " start ") {
			t.Fatalf("the apply touched a unit: %q", runner.calls)
		}
	}
	if len(journal.deferredScheduler)+len(journal.heldServices) != 0 {
		t.Fatalf("held %q deferred %q, want none", journal.heldServices, journal.deferredScheduler)
	}
}

// TestLayoutApplyStopsAtASignalAndRestartsEveryStoppedUnit delivers a real
// SIGTERM during the stop: the watch armed before it turns the signal into an
// interrupt, the apply runs no row, every stopped unit starts again (MCP in the
// apply, the scheduler after it) and the install answers 128 + SIGTERM.
func TestLayoutApplyStopsAtASignalAndRestartsEveryStoppedUnit(t *testing.T) {
	env := layoutFixture(t)
	legacies := layoutLegacyDatabases(t, env)
	var stderr bytes.Buffer
	journal := NewInstallJournal(context.Background(), env, &stderr)
	endSignalWatch(t, journal)
	runner := &layoutTestRunner{}
	signalled := false
	runner.onRun = func(call string) {
		if signalled || !strings.Contains(call, " stop ") && !strings.Contains(call, " bootout ") {
			return
		}
		signalled = true
		signalSelf(t, journal, syscall.SIGTERM)
	}
	env.runner = runner
	journal.env = env
	_, err := ApplyLayout(context.Background(), env, journal, true, &bytes.Buffer{})
	if !errors.Is(err, errInstallInterrupted) {
		t.Fatalf("err = %v, want the interrupt", err)
	}
	for _, legacy := range legacies {
		if _, err := os.Stat(legacy); err != nil {
			t.Fatalf("a row ran after the signal: %v", err)
		}
	}
	if journals, err := InstallJournals(env.Home); err != nil || len(journals) != 0 {
		t.Fatalf("journal written after the signal: %+v err=%v", journals, err)
	}
	if _, err := Run(
		context.Background(),
		Options{Mode: ModeApply, Journal: journal},
	); !errors.Is(
		err,
		errInstallInterrupted,
	) {
		t.Fatalf("Run after the signal: %v, want the interrupt before any write", err)
	}
	if code := journal.RestartSchedulerUnits(0, io.Discard); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("code = %d, want %d", code, 128+int(syscall.SIGTERM))
	}
	stopped, started := layoutServiceSets(runner.calls)
	slices.Sort(stopped)
	slices.Sort(started)
	want := layoutAllServices()
	slices.Sort(want)
	if !slices.Equal(stopped, want) || !slices.Equal(started, want) || journal.signals != nil {
		t.Fatalf("stopped %q started %q, want every unit %q back and the watch ended: %q",
			stopped, started, want, runner.calls)
	}
	if !strings.Contains(stderr.String(),
		"pfm install: interrupt received (terminated) — finishing the current step, then restarting the pfm services") {
		t.Fatalf("stderr %q, want the interrupt notice on the install's stderr", stderr.String())
	}
}

// endSignalWatch ends journal's signal watch when the test ends, whatever the
// test left armed.
func endSignalWatch(t *testing.T, journal *Journal) {
	t.Cleanup(func() {
		journal.servicesMu.Lock()
		defer journal.servicesMu.Unlock()
		journal.stopWatching()
	})
}

// signalSelf delivers sig to this process and waits, bounded, until journal's
// watch recorded an interrupt.
func signalSelf(t *testing.T, journal *Journal, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatalf("deliver %v: %v", sig, err)
	}
	for deadline := time.Now().Add(5 * time.Second); journal.interrupted.Load() == 0; {
		if time.Now().After(deadline) {
			t.Fatalf("the signal watch never received %v", sig)
		}
		time.Sleep(time.Millisecond)
	}
}

// layoutWantEveryUnitBack fails unless the calls stopped and started every
// unit in want.
func layoutWantEveryUnitBack(t *testing.T, calls, want []string) {
	t.Helper()
	stopped, started := layoutServiceSets(calls)
	slices.Sort(stopped)
	slices.Sort(started)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(stopped, want) || !slices.Equal(started, want) {
		t.Fatalf("stopped %q started %q, want every unit %q back: %q", stopped, started, want, calls)
	}
}

// TestLayoutApplyStopsAtASignalDuringTheMCPRestart delivers a SIGTERM while
// the apply restarts the MCP service after its last database row: the row
// that restart runs ahead of (a missing session-store link) never runs, and
// every unit is back.
func TestLayoutApplyStopsAtASignalDuringTheMCPRestart(t *testing.T) {
	env := layoutFixture(t)
	layoutLegacyDatabases(t, env)
	findings := ClassifyLayout(env)
	lastDB, _ := layoutDatabaseWork(findings)
	next := findings[lastDB+1]
	if next.Row != layoutRowSessionStore {
		t.Fatalf("the row after the last database row is %+v, want a session-store link", next)
	}
	if err := os.Remove(next.Path); err != nil {
		t.Fatal(err)
	}
	journal := NewInstallJournal(context.Background(), env, io.Discard)
	endSignalWatch(t, journal)
	runner := &layoutTestRunner{}
	signalled := false
	runner.onRun = func(call string) {
		mcpStart := strings.Contains(call, " start ") && strings.Contains(call, mcpUnitName) ||
			strings.Contains(call, " bootstrap ") && strings.Contains(call, mcpLaunchdLabel)
		if signalled || !mcpStart {
			return
		}
		signalled = true
		signalSelf(t, journal, syscall.SIGTERM)
	}
	env.runner = runner
	journal.env = env
	if _, err := ApplyLayout(context.Background(), env, journal, true, &bytes.Buffer{}); !errors.Is(
		err,
		errInstallInterrupted,
	) || !signalled {
		t.Fatalf("err = %v signalled %v, want the interrupt after the MCP restart", err, signalled)
	}
	if _, err := os.Lstat(next.Path); err == nil {
		t.Fatalf("the row after the MCP restart ran after the signal: %s linked", next.Path)
	}
	if code := journal.RestartSchedulerUnits(0, io.Discard); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("code = %d, want %d", code, 128+int(syscall.SIGTERM))
	}
	layoutWantEveryUnitBack(t, runner.calls, layoutAllServices())
}

// signalOnWrite is an apply's stdout that runs fire once, on the first write
// carrying match.
type signalOnWrite struct {
	match string
	fire  func()
}

func (writer *signalOnWrite) Write(line []byte) (int, error) {
	if writer.fire != nil && strings.Contains(string(line), writer.match) {
		fire := writer.fire
		writer.fire = nil
		fire()
	}
	return len(line), nil
}

// TestLayoutApplyAnswersASignalAfterItsLastRow delivers a SIGTERM once the
// last row (a stray directory) ran, past every row's check: the apply still
// answers the interrupt, so no later step of `pfm install` runs.
func TestLayoutApplyAnswersASignalAfterItsLastRow(t *testing.T) {
	env := layoutFixture(t)
	stray := filepath.Join(env.Home, ".cc", ".agents")
	if err := os.MkdirAll(stray, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := NewInstallJournal(context.Background(), env, io.Discard)
	endSignalWatch(t, journal)
	runner := &layoutTestRunner{}
	env.runner = runner
	journal.env = env
	stdout := &signalOnWrite{match: "ok      layout stray-dir " + stray}
	stdout.fire = func() { signalSelf(t, journal, syscall.SIGTERM) }
	if _, err := ApplyLayout(context.Background(), env, journal, true, stdout); !errors.Is(
		err,
		errInstallInterrupted,
	) || stdout.fire != nil {
		t.Fatalf("err = %v fired %v, want the interrupt after the last row", err, stdout.fire == nil)
	}
	if code := journal.RestartSchedulerUnits(0, io.Discard); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("code = %d, want %d", code, 128+int(syscall.SIGTERM))
	}
	layoutWantEveryUnitBack(t, runner.calls, layoutSchedulerServices())
}

// TestSignalWatchSwallowsASecondSignal proves a second signal is swallowed
// with the restart's promise, the first one kept.
func TestSignalWatchSwallowsASecondSignal(t *testing.T) {
	var stderr bytes.Buffer
	journal := NewInstallJournal(context.Background(), LayoutEnv{}, &stderr)
	journal.interrupt(syscall.SIGTERM, false)
	journal.interrupt(syscall.SIGINT, false)
	if journal.interrupted.Load() != int32(syscall.SIGTERM) || !strings.Contains(stderr.String(),
		"pfm install: interrupt — finishing the current step; the pfm services restart before pfm install exits") {
		t.Fatalf("interrupted %d stderr %q, want the first signal kept and the second swallowed",
			journal.interrupted.Load(), stderr.String())
	}
}

// TestSignalWatchKeepsASignalIgnoredAtLaunch proves a signal ignored at launch
// (nohup) stays ignored while the watch is armed.
func TestSignalWatchKeepsASignalIgnoredAtLaunch(t *testing.T) {
	signal.Ignore(syscall.SIGHUP)
	t.Cleanup(func() { signal.Reset(syscall.SIGHUP) })
	watched := NewInstallJournal(context.Background(), LayoutEnv{}, io.Discard)
	endSignalWatch(t, watched)
	watched.watchSignals()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("deliver SIGHUP: %v", err)
	}
	signalSelf(t, watched, syscall.SIGTERM)
	if got := syscall.Signal(watched.interrupted.Load()); got != syscall.SIGTERM {
		t.Fatalf("interrupt = %v, want the ignored SIGHUP dropped and SIGTERM recorded", got)
	}
}

// closedStdoutResult names the file the closed-stdout child reports into.
const closedStdoutResult = "PFM_TEST_CLOSED_STDOUT_RESULT"

// TestLayoutApplyWithAClosedStdoutRestartsEveryStoppedUnit runs a migrating
// apply in a child whose stdout is a pipe with no reader, as `pfm update`'s
// capture is once a Ctrl-C killed it: the write's SIGPIPE is an interrupt, not
// a kill, so the child still restarts every stopped unit and answers
// 128 + SIGPIPE.
func TestLayoutApplyWithAClosedStdoutRestartsEveryStoppedUnit(t *testing.T) {
	if result := os.Getenv(closedStdoutResult); result != "" {
		layoutApplyClosedStdoutChild(t, result)
		return
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(t.TempDir(), "result")
	child := exec.Command(os.Args[0], "-test.run=^TestLayoutApplyWithAClosedStdoutRestartsEveryStoppedUnit$")
	child.Env = append(os.Environ(), closedStdoutResult+"="+result)
	child.Stdout = writer
	var stderr bytes.Buffer
	child.Stderr = &stderr
	runErr := child.Run()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	verdict, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf(
			"the child left no verdict (%v, run %v, stderr %q): the closed stdout killed it",
			err,
			runErr,
			stderr.String(),
		)
	}
	if string(verdict) != "ok" {
		t.Fatalf("child: %s", verdict)
	}
}

// layoutApplyClosedStdoutChild is the child's half: it writes "ok" or what
// went wrong into result.
func layoutApplyClosedStdoutChild(t *testing.T, result string) {
	env := layoutFixture(t)
	layoutLegacyDatabases(t, env)
	runner := &layoutTestRunner{}
	env.runner = runner
	journal := NewInstallJournal(context.Background(), env, io.Discard)
	_, err := ApplyLayout(context.Background(), env, journal, true, os.Stdout)
	code := journal.RestartSchedulerUnits(0, io.Discard)
	stopped, started := layoutServiceSets(runner.calls)
	slices.Sort(stopped)
	slices.Sort(started)
	want := layoutAllServices()
	slices.Sort(want)
	verdict := "ok"
	if !errors.Is(err, errInstallInterrupted) || code != 128+int(syscall.SIGPIPE) ||
		!slices.Equal(stopped, want) || !slices.Equal(started, want) {
		verdict = fmt.Sprintf("err %v code %d stopped %q started %q, want the interrupt, %d and every unit %q back",
			err, code, stopped, started, 128+int(syscall.SIGPIPE), want)
	}
	if err := os.WriteFile(result, []byte(verdict), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLayoutApplyWithoutACloneTouchesNoUnit proves a row the apply only
// reports without a clone (errLayoutNoSource) is never layout work: a host
// with no clone probes, stops and restarts no unit, in the apply and its
// preview.
func TestLayoutApplyWithoutACloneTouchesNoUnit(t *testing.T) {
	env := layoutFixture(t)
	env.Clone = ""
	runner := &layoutTestRunner{}
	env.runner = runner
	findings := ClassifyLayout(env)
	if finding := layoutFindingByPath(findings, layoutRowZshrc, filepath.Join(env.Home, ".zshrc")); finding.Verdict ==
		VerdictOK {
		t.Fatalf("zshrc = %+v, want a row that needs the clone", finding)
	}
	runner.calls = nil
	if err := PreviewLayoutStop(context.Background(), env, findings); err != nil || len(runner.calls) != 0 {
		t.Fatalf("preview err %v calls %q, want no probe", err, runner.calls)
	}
	journal := NewJournal(context.Background(), env)
	if _, err := ApplyLayout(context.Background(), env, journal, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if stopped, started := layoutServiceSets(runner.calls); len(stopped)+len(started) != 0 ||
		len(journal.deferredScheduler)+len(journal.heldServices) != 0 {
		t.Fatalf("stopped %q started %q, want no unit touched: %q", stopped, started, runner.calls)
	}
}

// launchdTeardownRunner fails its first bootstraps as launchd does for a label
// still on its way out (EIO), and answers every print loaded.
type launchdTeardownRunner struct {
	failBootstraps int
	calls          []string
	printErr       error
}

func (runner *launchdTeardownRunner) Run(_ context.Context, name string, args ...string) error {
	runner.calls = append(runner.calls, name+" "+strings.Join(args, " "))
	if len(args) > 0 && args[0] == "bootstrap" && runner.failBootstraps > 0 {
		runner.failBootstraps--
		return errors.New("Bootstrap failed: 5: Input/output error")
	}
	if len(args) > 0 && args[0] == "print" {
		return runner.printErr
	}
	return nil
}

// TestRestartLaunchdLabelsRetriesABootstrapDuringTeardown proves a layout or
// deferred restart rides out a bootout still in flight.
func TestRestartLaunchdLabelsRetriesABootstrapDuringTeardown(t *testing.T) {
	runner := &launchdTeardownRunner{failBootstraps: 1}
	pauses := 0
	env := LayoutEnv{Home: t.TempDir(), settle: func(time.Duration) { pauses++ }}
	if err := restartLaunchdLabels(context.Background(), env, runner, []string{launchdLabel}, false); err != nil {
		t.Fatalf("restart after one EIO: %v (calls %q)", err, runner.calls)
	}
	if layoutCountCalls(runner.calls, "launchctl bootstrap") != 2 || pauses < 2 {
		t.Fatalf("calls %q pauses %d, want two bootstraps and a retry pause", runner.calls, pauses)
	}
}

// launchdExit is a launchctl exit status.
type launchdExit int

func (code launchdExit) Error() string { return fmt.Sprintf("exit status %d", int(code)) }
func (code launchdExit) ExitCode() int { return int(code) }

// TestRestartSchedulerLabelsBootstrapsEveryBootedOutLaunchdLabel proves the
// deferred launchd restart bootstraps every label this run booted out, even
// one a print reads loaded (a teardown still in flight); a bootstrap that
// keeps failing names the manual bootstrap, unless the stop settled and the
// label reads loaded: then Run already brought it back.
func TestRestartSchedulerLabelsBootstrapsEveryBootedOutLaunchdLabel(t *testing.T) {
	for _, testCase := range []struct {
		failBootstraps int
		settled, fails bool
	}{
		{},
		{failBootstraps: launchdBootstrapAttempts, fails: true},
		{failBootstraps: launchdBootstrapAttempts, settled: true},
	} {
		runner := &launchdTeardownRunner{failBootstraps: testCase.failBootstraps}
		env := LayoutEnv{Home: t.TempDir(), runner: runner, settle: func(time.Duration) {}}
		err := restartSchedulerLabels(context.Background(), env, []string{launchdLabel}, true, testCase.settled)
		if layoutCountCalls(runner.calls, "launchctl bootstrap") == 0 || (err != nil) != testCase.fails ||
			testCase.fails && !strings.Contains(err.Error(), "start it by hand: launchctl bootstrap gui/") {
			t.Fatalf("%+v: err %v calls %q, want a bootstrap and fails %v with the manual bootstrap",
				testCase, err, runner.calls, testCase.fails)
		}
	}
}

// TestServicesDownStartsAnUndecidedSystemdUnit proves a systemd unit whose
// state probe cannot answer is started anyway (start is idempotent), the probe
// error still reported.
func TestServicesDownStartsAnUndecidedSystemdUnit(t *testing.T) {
	env := LayoutEnv{runner: &layoutTestRunner{failProbe: true}}
	down, err := servicesDown(context.Background(), env, []string{nameSyncTimerUnit})
	if !slices.Equal(down, []string{nameSyncTimerUnit}) || err == nil {
		t.Fatalf("down %q err %v, want the undecided unit started and its error reported", down, err)
	}
}

// TestAwaitLaunchdTeardownRefusesALabelStillLoaded proves the stop waits,
// bounded, for launchd's teardown: a label still loaded after 25 s refuses
// naming it, leaving the rerun to its caller, and one that unloads passes.
func TestAwaitLaunchdTeardownRefusesALabelStillLoaded(t *testing.T) {
	pauses := 0
	env := LayoutEnv{settle: func(time.Duration) { pauses++ }}
	err := awaitLaunchdTeardown(context.Background(), env, &launchdTeardownRunner{}, []string{launchdLabel})
	if err == nil || !strings.Contains(err.Error(), launchdLabel) ||
		!strings.Contains(err.Error(), "still tearing down 25s") || strings.Contains(err.Error(), "rerun") ||
		pauses != launchdTeardownAttempts-1 {
		t.Fatalf("err %v after %d pauses, want the bounded teardown refusal", err, pauses)
	}
	runner := &launchdTeardownRunner{printErr: launchdExit(launchctlNotLoaded)}
	if err := awaitLaunchdTeardown(context.Background(), env, runner, []string{launchdLabel}); err != nil {
		t.Fatalf("an unloaded label: %v", err)
	}
}

// TestPreviewLayoutStopProbesTheMCPServiceWithDatabaseWork proves the preview
// with database work probes the MCP service the apply would stop, and refuses
// where that probe cannot answer.
func TestPreviewLayoutStopProbesTheMCPServiceWithDatabaseWork(t *testing.T) {
	findings := []LayoutFinding{
		{Row: layoutRowStateDB, Path: "/state/pfm.db", Source: "/legacy/fleet.db", Verdict: VerdictMove},
	}
	runner := &layoutTestRunner{}
	env := LayoutEnv{runner: runner}
	if err := PreviewLayoutStop(context.Background(), env, findings); err != nil {
		t.Fatal(err)
	}
	probe := layoutActiveStateProbe + mcpUnitName
	if schedulerIsLaunchd {
		probe = fmt.Sprintf("launchctl print gui/%d/%s", os.Getuid(), mcpLaunchdLabel)
	}
	if !slices.Contains(runner.calls, probe) {
		t.Fatalf("calls %q, want the MCP probe %q", runner.calls, probe)
	}
	if stopped, started := layoutServiceSets(runner.calls); len(stopped)+len(started) != 0 {
		t.Fatalf("the preview stopped %q started %q", stopped, started)
	}
	env.runner = &layoutTestRunner{failProbe: true}
	if schedulerIsLaunchd {
		env.runner = &launchdTeardownRunner{printErr: context.DeadlineExceeded}
	}
	if err := PreviewLayoutStop(context.Background(), env, findings); err == nil {
		t.Fatal("a probe that cannot answer passed the preview")
	}
}

// TestPreChangeRefusalsRefuseAServiceProbeTheStopCannotRead proves --check and
// the apply refuse before any write where the apply's service stop would, and
// probe nothing on a host with no layout work.
func TestPreChangeRefusalsRefuseAServiceProbeTheStopCannotRead(t *testing.T) {
	work := []LayoutFinding{{Row: layoutRowStagedPrompts, Path: "/managed/harness-prompts", Verdict: VerdictRemove}}
	advisory := []LayoutFinding{{Row: layoutRowManagedCleanup, Path: "/managed/pfm.json", Verdict: VerdictCreate}}
	for _, testCase := range []struct {
		findings []LayoutFinding
		want     int
	}{{findings: work, want: 1}, {findings: advisory, want: 0}, {findings: nil, want: 0}} {
		for _, check := range []bool{false, true} {
			systemd := &layoutTestRunner{failProbe: true}
			launchd := &launchdTeardownRunner{printErr: context.DeadlineExceeded}
			var runner CommandRunner = systemd
			calls := func() []string { return systemd.calls }
			if schedulerIsLaunchd {
				runner, calls = launchd, func() []string { return launchd.calls }
			}
			env := LayoutEnv{ConfigPath: filepath.Join(t.TempDir(), "pfm.config.json"), runner: runner}
			pre := PreChange{
				PlanMigration:     pfmconfig.PlanMigrationFrom,
				PrintDependencies: func(io.Writer, pfmconfig.Runtime) int { return 0 },
				Check:             check,
			}
			var stdout, stderr bytes.Buffer
			if code := PreChangeRefusals(env, testCase.findings, pre, &stdout, &stderr); code != testCase.want ||
				testCase.want != 0 && !strings.Contains(stderr.String(), "cannot be probed") {
				t.Fatalf("check=%v work=%v: code %d stderr=%q, want %d",
					check, testCase.findings != nil, code, stderr.String(), testCase.want)
			}
			stopped, started := layoutServiceSets(calls())
			if len(stopped)+len(started) != 0 {
				t.Fatalf("the preview stopped %q started %q", stopped, started)
			}
		}
	}
}

// TestLayoutServiceHeldDBWithTargetRefusesInTheGate proves a legacy database
// only a pfm service holds, whose target exists too, refuses in the gate as
// "both exist" instead of passing it to a refusal after the first write.
func TestLayoutServiceHeldDBWithTargetRefusesInTheGate(t *testing.T) {
	env := layoutFixture(t)
	env.runner = &layoutTestRunner{mainPIDs: map[string]int{layoutMCPService(): 4242}}
	layoutHeldLegacyStateDB(t, env, 4242, 1, "pfm")
	layoutWrite(t, env.StateDB, "target")
	finding := layoutFindingByPath(ClassifyLayout(env), layoutRowStateDB, env.StateDB)
	if finding.serviceHeld || finding.Detail != "target and legacy database both exist" {
		t.Fatalf("finding = %+v, want the both-exist refusal", finding)
	}
	var stdout, stderr bytes.Buffer
	if code := RunInstallCheck(env, ClassifyLayout(env), &stdout, &stderr); code != InstallCheckBlocked ||
		!strings.Contains(stdout.String()+stderr.String(), "target and legacy database both exist") {
		t.Fatalf("check code %d stdout=%q stderr=%q, want the gate's both-exist refusal",
			code, stdout.String(), stderr.String())
	}
}

// TestLayoutManagedCleanupNeverWritesThroughARelativeDir proves the advisory
// managed-cleanup row refuses a ManagedDir that is not absolute and writes
// nothing relative to the working directory.
func TestLayoutManagedCleanupNeverWritesThroughARelativeDir(t *testing.T) {
	env := layoutFixture(t)
	env.ManagedDir = ""
	env.runner = &layoutTestRunner{}
	t.Chdir(t.TempDir())
	finding := classifyManagedCleanup(env)
	if finding.Err != nil || finding.Verdict != VerdictRefuse || !strings.Contains(finding.Detail, "not absolute") {
		t.Fatalf("finding = %+v, want the not-absolute refusal", finding)
	}
	if _, err := ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{}); err != nil {
		t.Fatalf("an advisory refusal failed the apply: %v", err)
	}
	if _, err := os.Lstat("pfm.json"); err == nil {
		t.Fatal("pfm.json written relative to the working directory")
	}
}
