package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestLayoutDBHolderJudgedByServiceOwnership(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		services    map[string]int
		ppid        int
		comm        string
		serviceHeld bool
		detail      string
	}{
		{"service main process", map[string]int{"svc": 4242}, 1, "pfm", true, "held by pid 4242"},
		{"child of a service", map[string]int{"svc": 4100}, 4100, "sqlite3", true, "held by pid 4242"},
		{"no service running", nil, 1, "pfm", false, "held by pid 4242 (pfm) — close it"},
		{"another process tree", map[string]int{"svc": 4100}, 1, "pfm", false, "held by pid 4242 (pfm) — close it"},
		{"unnamed process", map[string]int{"svc": 4100}, 1, "", false, "held by pid 4242 (not a pfm service) — close it"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := layoutFixture(t)
			runner := &layoutTestRunner{mainPIDs: map[string]int{}}
			for _, pid := range testCase.services {
				runner.mainPIDs[layoutMCPService()] = pid
				layoutProcess(t, env, pid, 1, "pfm")
			}
			env.runner = runner
			layoutHeldLegacyStateDB(t, env, 4242, testCase.ppid, testCase.comm)
			findings := ClassifyLayout(env)
			finding := layoutFindingByPath(findings, layoutRowStateDB, env.StateDB)
			if finding.Err != nil || finding.Verdict != VerdictRefuse || finding.serviceHeld != testCase.serviceHeld ||
				finding.Detail != testCase.detail {
				t.Fatalf("finding=%+v, want serviceHeld=%v detail=%q", finding, testCase.serviceHeld, testCase.detail)
			}
			var stderr bytes.Buffer
			code := RunInstallCheck(env, findings, &bytes.Buffer{}, &stderr)
			if testCase.serviceHeld && code != 0 || !testCase.serviceHeld && code != InstallCheckBlocked {
				t.Fatalf("install check=%d stderr=%s", code, stderr.String())
			}
		})
	}
}

func TestLayoutDBHolderServiceProbeFailureIsUnreadable(t *testing.T) {
	env := layoutFixture(t)
	env.runner = &layoutTestRunner{garbagePID: true}
	layoutHeldLegacyStateDB(t, env, 4242, 1, "pfm")
	findings := ClassifyLayout(env)
	finding := layoutFindingByPath(findings, layoutRowStateDB, env.StateDB)
	if finding.Err == nil || !strings.Contains(finding.Err.Error(), "database holder 4242") || finding.serviceHeld {
		t.Fatalf("probe failure not an unreadable finding: %+v", finding)
	}
	var stderr bytes.Buffer
	if code := RunInstallCheck(env, findings, &bytes.Buffer{}, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "refuse  layout state-db "+env.StateDB+" — UNREADABLE") {
		t.Fatalf("install check=%d stderr=%s", code, stderr.String())
	}
}

func TestLaunchctlPrintPIDReadsTheJobPID(t *testing.T) {
	for _, testCase := range []struct {
		output string
		pid    int
		fails  bool
	}{
		{"gui/501/x = {\n\tstate = running\n\tpid = 90568\n}\n", 90568, false},
		{"gui/501/x = {\n\tstate = not running\n}\n", 0, false},
		{"\tpid = abc\n", 0, true},
	} {
		pid, err := launchctlPrintPID(testCase.output)
		if pid != testCase.pid || (err != nil) != testCase.fails {
			t.Fatalf("launchctlPrintPID(%q)=%d,%v", testCase.output, pid, err)
		}
	}
}

// layoutSessionMergeDir turns account 2's file-history into a real directory
// holding session/checkpoint, so its row merges; it returns the directory.
func layoutSessionMergeDir(t *testing.T, env LayoutEnv) string {
	t.Helper()
	path := filepath.Join(env.Config.Accounts[1].ConfigDir, "file-history")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(path, "session", "checkpoint"), "keep")
	return path
}

func TestLayoutSessionMergeMovesEntriesOtherUsersOwn(t *testing.T) {
	env := layoutFixture(t)
	path := layoutSessionMergeDir(t, env)
	// The fence cannot chown to another user, so the invoking uid moves
	// instead: every entry is then another uid's, in directories the user can
	// write, which a rename moves regardless of the owner.
	env.uid = func() int { return 3999999 }
	if finding := layoutFindingByPath(
		ClassifyLayout(env),
		layoutRowSessionStore,
		path,
	); finding.Verdict != VerdictMerge ||
		finding.Err != nil {
		t.Fatalf("entries in writable directories do not merge: %+v", finding)
	}
}

func TestLayoutSessionMergeRefusesRootInvoker(t *testing.T) {
	env := layoutFixture(t)
	path := layoutSessionMergeDir(t, env)
	if os.Geteuid() == 0 {
		// sudo keeps HOME: root would merge another user's session store.
		if err := os.Chown(path, 3999999, 3999999); err != nil {
			t.Fatal(err)
		}
	}
	env.uid = func() int { return 0 }
	finding := layoutFindingByPath(ClassifyLayout(env), layoutRowSessionStore, path)
	if finding.Err != nil || finding.Verdict != VerdictRefuse ||
		finding.Detail != "run pfm install as your own user, not root" {
		t.Fatalf("root invoker: %+v", finding)
	}
}

func TestLayoutSessionMergeRefusesEntriesTheUserCannotMove(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		denied func(path string) string
		mode   uint32
		count  int
		fix    func(path string) string
	}{
		{
			"unwritable directory", func(path string) string { return filepath.Join(path, "session") }, unix.W_OK, 2,
			func(path string) string { return path },
		},
		{
			"unreadable subtree", func(path string) string { return filepath.Join(path, "session") }, unix.R_OK, 1,
			func(path string) string { return path },
		},
		{
			"unreadable file", func(path string) string { return filepath.Join(path, "session", "checkpoint") },
			unix.R_OK, 1, func(path string) string { return path },
		},
		{"unwritable parent", filepath.Dir, unix.W_OK, 1, filepath.Dir},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := layoutFixture(t)
			path := layoutSessionMergeDir(t, env)
			denied := testCase.denied(path)
			// The fence runs as root, which access(2) never denies: the seam
			// denies what a non-root user's modes would.
			env.access = func(candidate string, mode uint32) error {
				if candidate == denied && mode&testCase.mode != 0 {
					return unix.EACCES
				}
				return nil
			}
			other := 3999999
			env.uid = func() int { return other }
			name := strconv.Itoa(other)
			fix := shellCommandLine(testCase.fix(path))
			want := strconv.Itoa(testCase.count) + " entries pfm cannot move as " + name + " (e.g. " + denied +
				") — run: sudo chown -R " + name + " " + fix + " && chmod -R u+rwX " + fix
			if testCase.name == "unwritable directory" || testCase.name == "unwritable parent" {
				want = strings.Replace(want, "(e.g. "+denied+")", "(e.g. "+map[bool]string{
					true: path, false: denied,
				}[testCase.name == "unwritable parent"]+")", 1)
			}
			finding := layoutFindingByPath(ClassifyLayout(env), layoutRowSessionStore, path)
			if finding.Err != nil || finding.Verdict != VerdictRefuse || finding.Detail != want {
				t.Fatalf("finding=%+v, want refuse %q", finding, want)
			}
			var output bytes.Buffer
			dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
			if err == nil || !strings.Contains(err.Error(), "refuse  layout session-store "+path+" — "+want) ||
				dir != "" {
				t.Fatalf("move refusal: dir=%q err=%v output=%s", dir, err, output.String())
			}
		})
	}
}

// layoutHeldLegacyStateDB swaps the fixture's state database for a legacy one
// held open by pid, whose parent is ppid and whose argv[0] is comm ("" writes
// no cmdline); it returns the legacy path.
func layoutHeldLegacyStateDB(t *testing.T, env LayoutEnv, pid, ppid int, comm string) string {
	t.Helper()
	if err := os.Remove(env.StateDB); err != nil {
		t.Fatal(err)
	}
	legacy := paths.LegacyStateDB(env.Home)
	db, err := sqlitedb.OpenStore(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	layoutProcess(t, env, pid, ppid, comm)
	fd := filepath.Join(env.ProcRoot, strconv.Itoa(pid), "fd")
	if err := os.MkdirAll(fd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacy, filepath.Join(fd, "3")); err != nil {
		t.Fatal(err)
	}
	return legacy
}

// layoutProcess writes a fixture process's stat (its parent) and, unless comm
// is "", its cmdline.
func layoutProcess(t *testing.T, env LayoutEnv, pid, ppid int, comm string) {
	t.Helper()
	stat := fmt.Sprintf("%d (%s) S %d%s\n", pid, comm, ppid, strings.Repeat(" 0", 20))
	layoutWrite(t, filepath.Join(env.ProcRoot, strconv.Itoa(pid), "stat"), stat)
	if comm != "" {
		layoutWrite(
			t,
			filepath.Join(env.ProcRoot, strconv.Itoa(pid), "cmdline"),
			"/usr/local/bin/"+comm+"\x00picker\x00",
		)
	}
}

// layoutMCPService is the service key the fake runner's mainPIDs reads on
// this kernel.
func layoutMCPService() string {
	if schedulerIsLaunchd {
		return mcpLaunchdLabel
	}
	return mcpUnitName
}

func layoutNoHostWrite(t *testing.T, env LayoutEnv, zshrc, zshrcBefore, legacy string) {
	t.Helper()
	if journals, err := InstallJournals(env.Home); err != nil || len(journals) != 0 {
		t.Fatalf("journal written before the refusal: %+v err=%v", journals, err)
	}
	if got, err := os.ReadFile(zshrc); err != nil || string(got) != zshrcBefore {
		t.Fatalf("zshrc changed before the refusal: %q err=%v", got, err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("held legacy DB moved: %v", err)
	}
	if _, err := os.Stat(env.StateDB); err == nil {
		t.Fatalf("state DB created before the refusal")
	}
}

func TestLayoutNonServiceDBHolderRefusesInTheGateBeforeAnyWrite(t *testing.T) {
	env := layoutFixture(t)
	runner := &layoutTestRunner{mainPIDs: map[string]int{layoutMCPService(): 4100}}
	env.runner = runner
	legacy := layoutHeldLegacyStateDB(t, env, 4242, 1, "pfm")
	zshrc := filepath.Join(env.Home, ".zshrc")
	zshrcBefore := sourceLine(filepath.Join(env.ManagedRoot, "shim", "pfm.zsh")) + "\n"
	layoutWrite(t, zshrc, zshrcBefore)
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	want := "refused before any change:\n  refuse  layout state-db " + env.StateDB +
		" — held by pid 4242 (pfm) — close it"
	if err == nil || !strings.Contains(err.Error(), want) || dir != "" || output.Len() != 0 {
		t.Fatalf("non-service holder: dir=%q err=%v output=%s", dir, err, output.String())
	}
	if stops := layoutCountCalls(runner.calls, " stop ") + layoutCountCalls(runner.calls, " bootout "); stops != 0 {
		t.Fatalf("gate refusal stopped services: %q", runner.calls)
	}
	layoutNoHostWrite(t, env, zshrc, zshrcBefore, legacy)
}

func TestLayoutHolderLeftAfterServiceStopRefusesBeforeAnyWrite(t *testing.T) {
	env := layoutFixture(t)
	// The gate sees the MCP service's own child holding the database; the
	// fake stop takes the service down but leaves the holder, as a process
	// that outlives its service or appears between gate and move would.
	runner := &layoutTestRunner{mainPIDs: map[string]int{layoutMCPService(): 4100}}
	env.runner = runner
	layoutProcess(t, env, 4100, 1, "pfm")
	legacy := layoutHeldLegacyStateDB(t, env, 4242, 4100, "sqlite3")
	if finding := layoutFindingByPath(ClassifyLayout(env), layoutRowStateDB, env.StateDB); !finding.serviceHeld ||
		finding.Err != nil || finding.Detail != "held by pid 4242" {
		t.Fatalf("service child holder not service-owned: %+v", finding)
	}
	zshrc := filepath.Join(env.Home, ".zshrc")
	zshrcBefore := sourceLine(filepath.Join(env.ManagedRoot, "shim", "pfm.zsh")) + "\n"
	layoutWrite(t, zshrc, zshrcBefore)
	var output bytes.Buffer
	journal := NewJournal(context.Background(), env)
	dir, err := ApplyLayout(context.Background(), env, journal, true, &output)
	want := "  refuse  layout state-db " + env.StateDB + " — held by pid 4242 with the pfm services stopped — close it"
	if err == nil || !strings.Contains(err.Error(), "refused before any change:\n"+want) || dir != "" ||
		output.Len() != 0 {
		t.Fatalf("holder after stop: dir=%q err=%v output=%s", dir, err, output.String())
	}
	layoutNoHostWrite(t, env, zshrc, zshrcBefore, legacy)
	stopped, started := layoutServiceSets(runner.calls)
	if !slices.Equal(stopped, layoutAllServices()) || !slices.Equal(started, []string{layoutMCPService()}) ||
		!slices.Equal(journal.deferredScheduler, layoutSchedulerServices()) {
		t.Fatalf("stopped %q started %q deferred %q, want all stopped once, the MCP service restarted, "+
			"the scheduler deferred: %q", stopped, started, journal.deferredScheduler, runner.calls)
	}
}

// layoutSchedulerServices are the name-sync scheduler services an apply
// stops and defers to Journal.RestartSchedulerUnits on this kernel.
func layoutSchedulerServices() []string {
	if schedulerIsLaunchd {
		return []string{launchdLabel}
	}
	return []string{nameSyncPathUnit, nameSyncTimerUnit}
}

// layoutAllServices is every pfm service stopLayoutServices stops on this
// kernel when all of them run: the launch agent labels, or the systemd units.
func layoutAllServices() []string {
	if schedulerIsLaunchd {
		return []string{mcpLaunchdLabel, launchdLabel}
	}
	return layoutServiceUnits
}

// layoutServiceSets reads the services each stop and each start named, in
// call order: systemd stops and starts every unit in one call, launchd boots
// out and bootstraps one label per call.
func layoutServiceSets(calls []string) (stopped, started []string) {
	for _, call := range calls {
		fields := strings.Fields(call)
		switch {
		case len(fields) > 2 && fields[0] == "systemctl" && fields[2] == "stop":
			stopped = append(stopped, fields[3:]...)
		case len(fields) > 2 && fields[0] == "systemctl" && fields[2] == "start":
			started = append(started, fields[3:]...)
		case len(fields) == 3 && fields[0] == "launchctl" && fields[1] == "bootout":
			stopped = append(stopped, filepath.Base(fields[2]))
		case len(fields) == 4 && fields[0] == "launchctl" && fields[1] == "bootstrap":
			started = append(started, strings.TrimSuffix(filepath.Base(fields[3]), ".plist"))
		}
	}
	return stopped, started
}

func TestLayoutFailedServiceStopRefusesBeforeAnyWrite(t *testing.T) {
	env := layoutFixture(t)
	legacies := layoutLegacyDatabases(t, env)
	runner := &layoutTestRunner{failStop: true}
	env.runner = runner
	zshrc := filepath.Join(env.Home, ".zshrc")
	zshrcBefore := sourceLine(filepath.Join(env.ManagedRoot, "shim", "pfm.zsh")) + "\n"
	layoutWrite(t, zshrc, zshrcBefore)
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	stopError := "systemctl --user stop layout services: " + os.ErrPermission.Error()
	if schedulerIsLaunchd {
		stopError = "launchctl bootout gui/" + strconv.Itoa(os.Getuid()) + "/" + mcpLaunchdLabel + ": " +
			os.ErrPermission.Error()
	}
	if err == nil || !strings.HasPrefix(err.Error(), "refused before any change:") ||
		!strings.Contains(err.Error(), stopError) || strings.Contains(err.Error(), "with the pfm services stopped") ||
		dir != "" || output.Len() != 0 {
		t.Fatalf("failed stop: dir=%q err=%v output=%s", dir, err, output.String())
	}
	layoutNoHostWrite(t, env, zshrc, zshrcBefore, legacies[0])
	if _, err := os.Stat(legacies[1]); err != nil {
		t.Fatalf("legacy cache DB moved: %v", err)
	}
	// systemd cannot say which units a failed stop took down, so the restart
	// starts the MCP service it asked to stop (the scheduler waits for
	// Journal.RestartSchedulerUnits); a launch agent whose bootout failed was
	// never stopped and is not bootstrapped.
	_, started := layoutServiceSets(runner.calls)
	if schedulerIsLaunchd && len(started) != 0 ||
		!schedulerIsLaunchd && !slices.Equal(started, []string{mcpUnitName}) {
		t.Fatalf("restart after the failed stop started %q: %q", started, runner.calls)
	}
}

func TestLayoutDBHolderGoneBeforeTheWalkIsDropped(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		service int
	}{{"no service running", 0}, {"another service running", 4100}} {
		t.Run(testCase.name, func(t *testing.T) {
			env := layoutFixture(t)
			runner := &layoutTestRunner{mainPIDs: map[string]int{}}
			if testCase.service != 0 {
				runner.mainPIDs[layoutMCPService()] = testCase.service
				layoutProcess(t, env, testCase.service, 1, "pfm")
			}
			env.runner = runner
			layoutHeldLegacyStateDB(t, env, 4242, 1, "pfm")
			// The holder exits between the fd scan and the parent walk: its
			// stat is gone, as /proc drops it.
			for _, name := range []string{"stat", "cmdline"} {
				if err := os.Remove(filepath.Join(env.ProcRoot, "4242", name)); err != nil {
					t.Fatal(err)
				}
			}
			findings := ClassifyLayout(env)
			finding := layoutFindingByPath(findings, layoutRowStateDB, env.StateDB)
			if finding.Err != nil || strings.Contains(finding.Detail, "close it") || !finding.serviceHeld {
				t.Fatalf("gone holder not dropped: %+v", finding)
			}
			var stderr bytes.Buffer
			if code := RunInstallCheck(env, findings, &bytes.Buffer{}, &stderr); code != 0 {
				t.Fatalf("install check=%d stderr=%s", code, stderr.String())
			}
		})
	}
}

// layoutPrintRunner answers `launchctl print` per label with an output or an
// error.
type layoutPrintRunner struct {
	outputs map[string]string
	errs    map[string]error
}

func (layoutPrintRunner) Run(context.Context, string, ...string) error {
	return errors.New("layoutPrintRunner: no Run")
}

func (runner layoutPrintRunner) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	label := filepath.Base(args[len(args)-1])
	return []byte(runner.outputs[label]), runner.errs[label]
}

func TestLaunchdServicePIDsFailsOnAPrintThatCannotAnswer(t *testing.T) {
	running := map[string]string{launchdLabel: "x = {\n\tpid = 77\n}\n"}
	notLoaded := commandExitError{name: "launchctl", code: 113, text: "Could not find service"}
	services, err := launchdServicePIDs(context.Background(), layoutPrintRunner{
		outputs: running, errs: map[string]error{mcpLaunchdLabel: notLoaded},
	})
	if err != nil || len(services) != 1 || services[77] != launchdLabel {
		t.Fatalf("not-loaded job: services=%v err=%v", services, err)
	}
	for _, failure := range []error{
		commandExitError{name: "launchctl", code: 1, text: "timed out"},
		context.DeadlineExceeded,
	} {
		services, err := launchdServicePIDs(context.Background(), layoutPrintRunner{
			outputs: running, errs: map[string]error{mcpLaunchdLabel: failure},
		})
		if err == nil || !strings.Contains(err.Error(), "launchctl print gui/") {
			t.Fatalf("print failure %v read as no service: services=%v err=%v", failure, services, err)
		}
	}
}

func TestLayoutServiceHeldDBMovesOnceTheServiceStops(t *testing.T) {
	env := layoutFixture(t)
	runner := &layoutTestRunner{mainPIDs: map[string]int{layoutMCPService(): 4242}}
	legacy := layoutHeldLegacyStateDB(t, env, 4242, 1, "pfm")
	runner.onRun = func(call string) {
		if strings.Contains(call, " stop ") || strings.Contains(call, " bootout ") {
			if err := os.RemoveAll(filepath.Join(env.ProcRoot, "4242")); err != nil {
				t.Error(err)
			}
		}
	}
	env.runner = runner
	var output bytes.Buffer
	if _, err := ApplyLayout(context.Background(), env, nil, true, &output); err != nil {
		t.Fatalf("service-held apply: %v output=%s", err, output.String())
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Fatalf("legacy DB still in place after the move")
	}
	if _, err := os.Stat(env.StateDB); err != nil {
		t.Fatalf("state DB not moved: %v", err)
	}
}

// layoutWalkProcFS answers Stat from a parent table: a pid in errs fails with
// its error, a pid in neither is gone.
type layoutWalkProcFS struct {
	parents map[int]int
	errs    map[int]error
}

func (layoutWalkProcFS) PIDs() ([]int, error)                   { return nil, nil }
func (layoutWalkProcFS) Cmdline(int) ([]string, error)          { return nil, nil }
func (layoutWalkProcFS) Environ(int) (map[string]string, error) { return nil, nil }
func (layoutWalkProcFS) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }

func (processes layoutWalkProcFS) Stat(pid int) (gather.ProcStat, error) {
	if err, ok := processes.errs[pid]; ok {
		return gather.ProcStat{}, err
	}
	parent, ok := processes.parents[pid]
	if !ok {
		return gather.ProcStat{}, fmt.Errorf("pid %d: %w", pid, fs.ErrNotExist)
	}
	return gather.ProcStat{ParentPID: parent}, nil
}

func TestServiceOwnedPIDWalksToAServiceOrDropsAGoneHolder(t *testing.T) {
	service := map[int]string{100: mcpUnitName}
	unreadable := errors.New("read kern.proc.pid for 300: input/output error")
	for _, testCase := range []struct {
		name      string
		services  map[int]string
		processes layoutWalkProcFS
		want      holderOwner
		fails     bool
	}{
		{"descendant of a service", service, layoutWalkProcFS{parents: map[int]int{300: 200, 200: 100}}, holderService, false},
		{"holder gone", service, layoutWalkProcFS{}, holderGone, false},
		{"holder gone, no service", nil, layoutWalkProcFS{}, holderGone, false},
		{"ancestor gone mid-walk", service, layoutWalkProcFS{parents: map[int]int{300: 200}}, holderOther, false},
		{"another tree", service, layoutWalkProcFS{parents: map[int]int{300: 1}}, holderOther, false},
		{"live holder, no service", nil, layoutWalkProcFS{parents: map[int]int{300: 1}}, holderOther, false},
		{"live holder unreadable", service, layoutWalkProcFS{errs: map[int]error{300: unreadable}}, holderOther, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			owner, err := serviceOwnedPID(testCase.processes, testCase.services, 300)
			if owner != testCase.want || (err != nil) != testCase.fails {
				t.Fatalf("serviceOwnedPID = %v, %v; want %v, fails=%v", owner, err, testCase.want, testCase.fails)
			}
		})
	}
}
