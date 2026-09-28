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
	var output bytes.Buffer
	if _, err := ApplyLayout(context.Background(), env, nil, true, &output); err != nil {
		t.Fatalf("apply: %v output=%s", err, output.String())
	}
	got := layoutSystemctlCalls(runner.calls)
	if schedulerIsLaunchd {
		if layoutCountCalls(got, "launchctl bootout ") != 2 || layoutCountCalls(got, "launchctl bootstrap ") != 2 {
			t.Fatalf("launchd calls = %q, want one bootout pair and one bootstrap pair", got)
		}
		return
	}
	want := append([]string{"systemctl --user show-environment"}, layoutProbeCalls()...)
	want = append(want, "systemctl --user stop "+fleetUnitList, "systemctl --user start "+fleetUnitList)
	want = append(want, layoutProbeCalls()...)
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
	if err == nil {
		t.Fatal("apply with an unreadable unit state succeeded")
	}
	for index, row := range []string{layoutRowStateDB, layoutRowCacheDB} {
		target := []string{env.StateDB, env.CacheDB}[index]
		want := fmt.Sprintf("layout %s %s: %s", row, target, layoutActiveStateProbe+mcpUnitName)
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
		if _, statErr := os.Stat(legacies[index]); statErr != nil {
			t.Fatalf("%s moved without a stop: %v", legacies[index], statErr)
		}
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
	if got := layoutSystemctlCalls(runner.calls); !slices.Equal(got, []string{"systemctl --user show-environment"}) {
		t.Fatalf("systemctl calls = %q, want only the manager check", got)
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
	stop, start := " stop ", " start "
	if schedulerIsLaunchd {
		stop, start = " bootout ", " bootstrap "
	}
	stops := layoutCountCalls(runner.calls, stop)
	if stops == 0 || layoutCountCalls(runner.calls, start) != stops {
		t.Fatalf("stopped units not started exactly once: %q", runner.calls)
	}
}

func TestLayoutApplyWithoutDatabaseWorkTouchesNoUnit(t *testing.T) {
	env := layoutFixture(t)
	runner := &layoutTestRunner{}
	env.runner = runner
	if _, err := ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := layoutSystemctlCalls(runner.calls); len(got) != 0 {
		t.Fatalf("service calls without database work: %q", got)
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
