package installer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

const fleetUnitList = "pfm-mcp.service pfm-name-sync.path pfm-name-sync.timer"

// layoutLegacyDatabases replaces the fixture's migrated databases with legacy
// ones, so both database rows plan a move; it returns the legacy paths.
func layoutLegacyDatabases(t *testing.T, env LayoutEnv) []string {
	t.Helper()
	legacies := []string{paths.LegacyStateDB(env.Home), paths.LegacyCacheDB(env.Home)}
	for index, target := range []string{env.StateDB, env.CacheDB} {
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		db, err := sqlitedb.OpenStore(context.Background(), legacies[index])
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return legacies
}

// layoutSystemctlCalls keeps the service manager calls of a run.
func layoutSystemctlCalls(calls []string) []string {
	var kept []string
	for _, call := range calls {
		if strings.HasPrefix(call, "systemctl ") || strings.HasPrefix(call, "launchctl ") {
			kept = append(kept, call)
		}
	}
	return kept
}

func layoutCountCalls(calls []string, verb string) int {
	count := 0
	for _, call := range calls {
		if strings.Contains(call, verb) {
			count++
		}
	}
	return count
}

func layoutProbeCalls() []string {
	var probes []string
	for _, unit := range layoutServiceUnits {
		probes = append(probes, layoutActiveStateProbe+unit)
	}
	return probes
}

func TestLayoutApplyStopsFleetOnceAcrossBothDatabases(t *testing.T) {
	env := layoutFixture(t)
	legacies := layoutLegacyDatabases(t, env)
	runner := &layoutTestRunner{}
	runner.onRun = func(call string) {
		for _, path := range legacies {
			if strings.Contains(call, " stop ") {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("fleet stopped after %s moved: %v", path, err)
				}
			}
		}
		for _, path := range []string{env.StateDB, env.CacheDB} {
			if strings.Contains(call, " start ") {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("fleet started before %s moved: %v", path, err)
				}
			}
		}
	}
	env.runner = runner
	journal := NewJournal(context.Background(), env)
	var output bytes.Buffer
	if _, err := ApplyLayout(context.Background(), env, journal, true, &output); err != nil {
		t.Fatalf("apply: %v output=%s", err, output.String())
	}
	// Only the MCP service restarts inside the apply; the scheduler waits for
	// Journal.RestartSchedulerUnits, after installer.Run.
	if !slices.Equal(journal.deferredScheduler, layoutSchedulerServices()) {
		t.Fatalf("deferred scheduler = %q, want %q", journal.deferredScheduler, layoutSchedulerServices())
	}
	got := layoutSystemctlCalls(runner.calls)
	if schedulerIsLaunchd {
		if layoutCountCalls(got, "launchctl bootout ") != 2 || layoutCountCalls(got, "launchctl bootstrap ") != 1 {
			t.Fatalf("launchd calls = %q, want a bootout per label and one MCP bootstrap", got)
		}
		return
	}
	want := append([]string{"systemctl --user show-environment"}, layoutProbeCalls()...)
	// The name-sync job is asked again once its schedule is down.
	want = append(want, "systemctl --user stop "+fleetUnitList, layoutActiveStateProbe+nameSyncServiceUnit,
		"systemctl --user start "+mcpUnitName, layoutActiveStateProbe+mcpUnitName)
	if !slices.Equal(got, want) {
		t.Fatalf("systemctl calls = %q, want %q", got, want)
	}
}

func TestLayoutApplyStopsOnlyRunningUnits(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		states map[string]string
		want   []string
	}{
		{
			name: "activating",
			states: map[string]string{
				mcpUnitName: "activating", nameSyncPathUnit: "inactive", nameSyncTimerUnit: "inactive",
			},
			want: []string{"systemctl --user stop pfm-mcp.service", "systemctl --user start pfm-mcp.service"},
		},
		{
			name: "all stopped",
			states: map[string]string{
				mcpUnitName: "inactive", nameSyncPathUnit: "inactive", nameSyncTimerUnit: "inactive",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if schedulerIsLaunchd {
				return
			}
			env := layoutFixture(t)
			layoutLegacyDatabases(t, env)
			runner := &layoutTestRunner{states: testCase.states}
			env.runner = runner
			if _, err := ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if got := layoutServiceLifecycle(runner.calls); !slices.Equal(got, testCase.want) {
				t.Fatalf("lifecycle = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestLayoutApplyProbeFailureMovesNoDatabase(t *testing.T) {
	if schedulerIsLaunchd {
		return
	}
	env := layoutFixture(t)
	legacies := layoutLegacyDatabases(t, env)
	runner := &layoutTestRunner{failProbe: true}
	env.runner = runner
	_, err := ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "refused before any change:") ||
		!strings.Contains(err.Error(), layoutActiveStateProbe+mcpUnitName) {
		t.Fatalf("unreadable unit state did not refuse the whole apply naming the probe: %v", err)
	}
	for _, legacy := range legacies {
		if _, statErr := os.Stat(legacy); statErr != nil {
			t.Fatalf("%s moved without a stop: %v", legacy, statErr)
		}
	}
	if journals, err := InstallJournals(env.Home); err != nil || len(journals) != 0 {
		t.Fatalf("journal written before the refusal: %+v err=%v", journals, err)
	}
	if layoutCountCalls(runner.calls, " stop ") != 0 || layoutCountCalls(runner.calls, " start ") != 0 {
		t.Fatalf("units touched after a failed probe: %q", runner.calls)
	}
}

func TestLayoutApplyWithoutUserManagerStopsNothing(t *testing.T) {
	if schedulerIsLaunchd {
		return
	}
	env := layoutFixture(t)
	layoutLegacyDatabases(t, env)
	runner := &layoutTestRunner{noManager: true}
	env.runner = runner
	if _, err := ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply without a user manager: %v", err)
	}
	// The manager check, then the name-sync job's read-only re-ask: no unit
	// is stopped or started.
	want := []string{"systemctl --user show-environment", layoutActiveStateProbe + nameSyncServiceUnit}
	if got := layoutSystemctlCalls(runner.calls); !slices.Equal(got, want) {
		t.Fatalf("systemctl calls = %q, want only the manager check and the job probe", got)
	}
	if _, err := os.Stat(env.StateDB); err != nil {
		t.Fatalf("holder scan did not decide the move: %v", err)
	}
}

func TestLayoutApplyReportsUnitNotBackAfterStart(t *testing.T) {
	if schedulerIsLaunchd {
		return
	}
	env := layoutFixture(t)
	layoutLegacyDatabases(t, env)
	runner := &layoutTestRunner{afterStart: map[string]string{mcpUnitName: "activating"}}
	env.runner = runner
	_, err := ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{})
	want := "fleet unit pfm-mcp.service is activating 3s after start — journalctl --user -u pfm-mcp.service -n 20"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("apply error = %v, want %q", err, want)
	}
	for _, target := range []string{env.StateDB, env.CacheDB} {
		if _, statErr := os.Stat(target); statErr != nil {
			t.Fatalf("move of %s undone by the verify: %v", target, statErr)
		}
	}
}

func TestLayoutApplyEarlyReturnAfterStopStartsOnce(t *testing.T) {
	env := layoutFixture(t)
	layoutLegacyDatabases(t, env)
	// A config the state-db row's config migration cannot load returns from
	// the loop after the stop.
	layoutWrite(t, env.ConfigPath, "{")
	runner := &layoutTestRunner{}
	env.runner = runner
	_, err := ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "layout config migration") {
		t.Fatalf("apply error = %v, want the config migration failure", err)
	}
	stopped, started := layoutServiceSets(runner.calls)
	if !slices.Equal(stopped, layoutAllServices()) || !slices.Equal(started, []string{layoutMCPService()}) {
		t.Fatalf("stopped %q started %q, want all stopped and the MCP service started once: %q",
			stopped, started, runner.calls)
	}
}

// TestLayoutApplyOnAMigratedHostTouchesNoUnit proves a routine apply on a
// migrated host (no layout work) stops, defers and starts no unit.
func TestLayoutApplyOnAMigratedHostTouchesNoUnit(t *testing.T) {
	env := layoutFixture(t)
	runner := &layoutTestRunner{}
	env.runner = runner
	journal := NewJournal(context.Background(), env)
	if _, err := ApplyLayout(context.Background(), env, journal, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	stopped, started := layoutServiceSets(runner.calls)
	if len(stopped) != 0 || len(started) != 0 || len(journal.deferredScheduler) != 0 {
		t.Fatalf("stopped %q started %q deferred %q, want no unit touched: %q",
			stopped, started, journal.deferredScheduler, runner.calls)
	}
}

// TestLayoutApplyWithoutDatabaseWorkStopsOnlyTheScheduler proves layout work
// with no database to move stops the scheduler alone and defers it to
// RestartSchedulerUnits, which starts it after installer.Run.
func TestLayoutApplyWithoutDatabaseWorkStopsOnlyTheScheduler(t *testing.T) {
	env := layoutFixture(t)
	// A staged prompt directory is layout work that moves no database.
	layoutStagedPrompts(t, env)
	runner := &layoutTestRunner{}
	env.runner = runner
	journal := NewJournal(context.Background(), env)
	if _, err := ApplyLayout(context.Background(), env, journal, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	stopped, started := layoutServiceSets(runner.calls)
	if want := layoutSchedulerServices(); !slices.Equal(stopped, want) || len(started) != 0 ||
		!slices.Equal(journal.deferredScheduler, want) {
		t.Fatalf("stopped %q started %q deferred %q, want the scheduler %q stopped and deferred: %q",
			stopped, started, journal.deferredScheduler, want, runner.calls)
	}
	if code := journal.RestartSchedulerUnits(0, &bytes.Buffer{}); code != 0 {
		t.Fatalf("restart code = %d: %q", code, runner.calls)
	}
	if _, started := layoutServiceSets(runner.calls); !slices.Equal(started, layoutSchedulerServices()) {
		t.Fatalf("started %q after Run, want the scheduler: %q", started, runner.calls)
	}
	// A scheduler stop that fails refuses before the first write.
	env = layoutFixture(t)
	layoutStagedPrompts(t, env)
	env.runner = &layoutTestRunner{failStop: true}
	dir, err := ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "refused before any change:") || dir != "" {
		t.Fatalf("failed scheduler stop: dir=%q err=%v", dir, err)
	}
	if journals, err := InstallJournals(env.Home); err != nil || len(journals) != 0 {
		t.Fatalf("journal written before the refusal: %+v err=%v", journals, err)
	}
}

func TestUnitStateRunningReadsEveryState(t *testing.T) {
	for state, want := range map[string][2]bool{
		"active": {true, true}, "activating": {true, true}, "deactivating": {true, true},
		"reloading": {true, true}, "refreshing": {true, true},
		"inactive": {false, true}, "failed": {false, true}, "": {false, false}, "maintenance": {false, false},
	} {
		running, known := unitStateRunning(state)
		if running != want[0] || known != want[1] {
			t.Fatalf("unitStateRunning(%q) = (%v, %v), want %v", state, running, known, want)
		}
	}
}

func TestVerifyFleetUnitsActiveNamesUnreadableState(t *testing.T) {
	err := verifyFleetUnitsActive(context.Background(), &layoutTestRunner{failProbe: true}, []string{mcpUnitName})
	if err == nil || !strings.Contains(err.Error(), "fleet unit pfm-mcp.service state unreadable after start: ") {
		t.Fatalf("verify error = %v", err)
	}
}

// TestInstallReportsMCPRestartThatDoesNotComeBack: the install's own
// pfm-mcp.service restart is verified like a layout restart; a unit that does
// not come back fails the run after the later steps, never as a skip, and a
// later step that fails keeps the restart failure in the returned error.
func TestInstallReportsMCPRestartThatDoesNotComeBack(t *testing.T) {
	t.Parallel()
	if schedulerIsLaunchd {
		return
	}
	want := "fleet unit pfm-mcp.service is failed 3s after start — journalctl --user -u pfm-mcp.service -n 20"
	for _, laterFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("later step fails=%v", laterFails), func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			runner := &fakeRunner{manager: true, mcpState: "failed"}
			var output bytes.Buffer
			settled := time.Duration(0)
			_, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t),
				Mode:          ModeApply, Home: home, Runner: runner, Stdout: &output,
				MCPEnabled: map[string]bool{"chat": true},
				Sleep: func(d time.Duration) {
					settled += d
					if laterFails {
						// The ledger tears during the settle, after the plan read
						// it: wireVSCode, a later step, fails.
						layoutWrite(t, filepath.Join(managedRootForHome(home), vscodeOwnershipName), "{")
					}
				},
			})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("install error = %v, want %q\n%s", err, want, output.String())
			}
			if settled < fleetUnitSettle {
				t.Fatalf("restart verified after %s, want the %s settle", settled, fleetUnitSettle)
			}
			text := output.String()
			if strings.Contains(text, "skip    systemctl --user restart") {
				t.Fatalf("restart failure printed as a skip:\n%s", text)
			}
			failed := strings.Index(text, want)
			if !laterFails && (failed < 0 || !strings.Contains(text[failed:], "zshrc")) {
				t.Fatalf("later steps did not run after the failed restart:\n%s", text)
			}
		})
	}
}
