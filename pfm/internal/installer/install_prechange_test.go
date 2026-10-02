package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestConfigMoveSourceNamesOnlyAMoveIntoTheConfigPath(t *testing.T) {
	const target = "/tmp/demo-proj/pfm.config.json"
	move := LayoutFinding{Row: "config", Verdict: VerdictMove, Source: "/tmp/demo-proj/legacy.json", Path: target}
	cases := []struct {
		name    string
		finding func(LayoutFinding) LayoutFinding
		want    string
	}{
		{"a move into the path", func(f LayoutFinding) LayoutFinding { return f }, move.Source},
		{
			"a move elsewhere",
			func(f LayoutFinding) LayoutFinding { f.Path = "/tmp/demo-proj/other.json"; return f },
			"",
		},
		{"an unreadable row", func(f LayoutFinding) LayoutFinding { f.Err = errors.New("unreadable"); return f }, ""},
		{"no move", func(f LayoutFinding) LayoutFinding { f.Verdict = VerdictOK; return f }, ""},
		{"another row", func(f LayoutFinding) LayoutFinding { f.Row = "state-db"; return f }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConfigMoveSource([]LayoutFinding{tc.finding(move)}, target); got != tc.want {
				t.Fatalf("ConfigMoveSource = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDatabasePathsAgreeRefusesEitherPathThatDiffers(t *testing.T) {
	resolved := paths.Values{StateDB: "/tmp/demo-proj/state.db", CacheDB: "/tmp/demo-proj/cache.db"}
	if err := DatabasePathsAgree(resolved.StateDB, resolved.CacheDB, resolved); err != nil {
		t.Fatalf("agreeing paths refused: %v", err)
	}
	for _, pair := range [][2]string{{"/tmp/demo-proj/x.db", resolved.CacheDB}, {resolved.StateDB, "/tmp/demo-proj/x.db"}} {
		err := DatabasePathsAgree(pair[0], pair[1], resolved)
		if err == nil || !strings.Contains(err.Error(), "moved database paths differ from resolved paths") {
			t.Fatalf("DatabasePathsAgree(%q, %q) = %v, want the differ refusal", pair[0], pair[1], err)
		}
	}
}

func TestSchedulerRefusalNamesOnlyARunningJob(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{
			ErrNameSyncRunning,
			"pfm install: the pfm name-sync service is running; wait for it to finish or run `systemctl --user stop pfm-name-sync.service`, then retry",
		},
		{
			fmt.Errorf("gate: %w", ErrLaunchAgentRunning),
			"pfm install: the pfm name-sync launch agent is running; wait for it to finish or `launchctl bootout gui/$(id -u)/com.professor.pfm.name-sync` first",
		},
		{errors.New("other"), ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := SchedulerRefusal("install", tc.err); got != tc.want {
			t.Fatalf("SchedulerRefusal(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// TestPreChangeRefusalsHoldTheCheckAnswerBehindALaterRefusal proves a failed
// dependency preflight refuses both modes before any answer: --check never
// prints the gate's ok and moves the table to stderr; the apply prints it on stdout.
func TestPreChangeRefusalsHoldTheCheckAnswerBehindALaterRefusal(t *testing.T) {
	env := LayoutEnv{ConfigPath: filepath.Join(t.TempDir(), "pfm.config.json")}
	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprintf("check=%v", check), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			pre := PreChange{
				PlanMigration: func(pfmconfig.Config, string) (pfmconfig.Migration, error) {
					t.Fatal("no config to plan: the migration planner must not run")
					return pfmconfig.Migration{}, nil
				},
				PrintDependencies: func(w io.Writer, _ pfmconfig.Runtime) int {
					fmt.Fprintln(w, "fixture dependency table")
					return 1
				},
				Check: check,
			}
			if code := PreChangeRefusals(env, nil, pre, &stdout, &stderr); code != 1 {
				t.Fatalf("code = %d, want 1; stderr=%q", code, stderr.String())
			}
			table := &stdout
			if check {
				table = &stderr
			}
			if !strings.Contains(table.String(), "fixture dependency table") ||
				!strings.Contains(stderr.String(), "pfm install: required dependency preflight failed\n") ||
				strings.Contains(stdout.String(), "install check: ok") {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

// TestMovedDatabasePathsAgreeCountsAServiceHeldRowAsMoving proves the pre-write
// path check refuses a database only a pfm service holds: the apply stops that
// service and moves the row, so --check and --yes must refuse before it does.
func TestMovedDatabasePathsAgreeCountsAServiceHeldRowAsMoving(t *testing.T) {
	root := t.TempDir()
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "resolved-state.db"))
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "resolved-cache.db"))
	env := LayoutEnv{StateDB: filepath.Join(root, "state.db"), CacheDB: filepath.Join(root, "cache.db")}
	held := LayoutFinding{
		Row: "state-db", Verdict: VerdictRefuse, Path: env.StateDB,
		Source: filepath.Join(root, "fleet.db"), Detail: "held by pid 4242", serviceHeld: true,
	}
	err := MovedDatabasePathsAgree(env, []LayoutFinding{held})
	if err == nil || !strings.Contains(err.Error(), "moved database paths differ from resolved paths") {
		t.Fatalf("service-held row: MovedDatabasePathsAgree = %v, want the differ refusal", err)
	}
	held.serviceHeld = false
	if err := MovedDatabasePathsAgree(env, []LayoutFinding{held}); err != nil {
		t.Fatalf("a row the gate refuses moves nothing: MovedDatabasePathsAgree = %v, want nil", err)
	}
}

// TestPreChangeRefusalsRefuseARunningNameSyncJob proves the name-sync probe
// asks through the env's runner: a job running now refuses the apply with 97
// and --check with InstallCheckBlocked, before any answer prints.
func TestPreChangeRefusalsRefuseARunningNameSyncJob(t *testing.T) {
	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprintf("check=%v", check), func(t *testing.T) {
			env := LayoutEnv{
				ConfigPath: filepath.Join(t.TempDir(), "pfm.config.json"),
				runner: &outputRunner{
					fakeRunner:  fakeRunner{manager: true, nameSyncActive: true},
					printOutput: "state = running\n",
				},
			}
			var stdout, stderr bytes.Buffer
			pre := PreChange{
				PlanMigration:     pfmconfig.PlanMigrationFrom,
				PrintDependencies: func(io.Writer, pfmconfig.Runtime) int { return 0 },
				Check:             check,
			}
			want := 97
			if check {
				want = InstallCheckBlocked
			}
			if code := PreChangeRefusals(env, nil, pre, &stdout, &stderr); code != want {
				t.Fatalf("code = %d, want %d; stdout=%q stderr=%q", code, want, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "name-sync") ||
				strings.Contains(stdout.String(), "install check: ok") {
				t.Fatalf(
					"stdout=%q stderr=%q, want the running-job refusal and no ok",
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
}

// TestPreChangeRefusalsRefuseAStrayPreSplitConfigBeforeAnyWrite proves the
// layout's config-migration refusal (a pre-split config.json beside the
// config) answers before the first write in both modes, while a config.json
// the config row itself moves away is no stray.
func TestPreChangeRefusalsRefuseAStrayPreSplitConfigBeforeAnyWrite(t *testing.T) {
	for _, check := range []bool{false, true} {
		t.Run(fmt.Sprintf("check=%v", check), func(t *testing.T) {
			dir := t.TempDir()
			env := LayoutEnv{ConfigPath: filepath.Join(dir, pfmconfig.FileName)}
			for _, path := range []string{env.ConfigPath, filepath.Join(dir, pfmconfig.LegacyFileName)} {
				if err := os.WriteFile(path, []byte("{\"version\":2}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			pre := PreChange{
				PlanMigration: pfmconfig.PlanMigrationFrom,
				PrintDependencies: func(io.Writer, pfmconfig.Runtime) int {
					t.Fatal("the stray config refusal must precede the dependency preflight")
					return 0
				},
				Check: check,
			}
			if code := PreChangeRefusals(env, nil, pre, &stdout, &stderr); code != 1 {
				t.Fatalf("code = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "pre-split config path is outside HostLayout") ||
				strings.Contains(stdout.String(), "install check: ok") {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
	t.Run("moved away by the config row", func(t *testing.T) {
		dir := t.TempDir()
		env := LayoutEnv{ConfigPath: filepath.Join(dir, pfmconfig.FileName)}
		legacy := filepath.Join(dir, pfmconfig.LegacyFileName)
		if err := os.WriteFile(legacy, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		move := LayoutFinding{Row: "config", Verdict: VerdictMove, Source: legacy, Path: env.ConfigPath}
		refusal := refuseStrayConfig(pfmconfig.Migration{StrayLegacyPath: legacy}, move.Source)
		if refusal != nil {
			t.Fatalf("a config.json the config row moves away refused: %v", refusal)
		}
		if err := refuseStrayConfig(pfmconfig.Migration{StrayLegacyPath: legacy}, ""); err == nil {
			t.Fatal("a stray config.json nothing moves was not refused")
		}
	})
}

// TestRestartSchedulerUnitsStartsTheDeferredUnitsAfterRun proves the layout
// hands the name-sync scheduler units to the install, which starts after
// installer.Run only those still down, keeps Run's exit code, and turns a
// failed start into a failure.
func TestRestartSchedulerUnitsStartsTheDeferredUnitsAfterRun(t *testing.T) {
	service, scheduler := mcpUnitName, []string{nameSyncPathUnit, nameSyncTimerUnit}
	if schedulerIsLaunchd {
		service, scheduler = mcpLaunchdLabel, []string{launchdLabel}
	}
	newJournal := func(runner *layoutTestRunner) *Journal {
		return NewJournal(
			context.Background(),
			LayoutEnv{Home: t.TempDir(), runner: runner, settle: func(time.Duration) {}},
		)
	}
	runner := &layoutTestRunner{states: map[string]string{nameSyncPathUnit: "inactive", nameSyncTimerUnit: "active"}}
	journal := newJournal(runner)
	journal.holdServices(append([]string{service}, scheduler...), true)
	if !slices.Equal(journal.heldServices, []string{service}) {
		t.Fatalf("ApplyLayout restarts %v, want only %s", journal.heldServices, service)
	}
	journal.heldServices = nil
	var stderr bytes.Buffer
	if code := journal.RestartSchedulerUnits(97, &stderr); code != 97 || stderr.Len() != 0 {
		t.Fatalf("code = %d stderr=%q, want Run's 97 kept", code, stderr.String())
	}
	want := []string{"systemctl --user start " + nameSyncPathUnit}
	if schedulerIsLaunchd {
		want = nil // launchctl print answers loaded: Run already bootstrapped it
	}
	var starts []string
	for _, call := range layoutServiceLifecycle(runner.calls) {
		if strings.Contains(call, " start ") || strings.Contains(call, " bootstrap ") {
			starts = append(starts, call)
		}
	}
	if !slices.Equal(starts, want) {
		t.Fatalf("starts = %q, want %q (calls %q)", starts, want, runner.calls)
	}
	before := len(runner.calls)
	if code := journal.RestartSchedulerUnits(0, &stderr); code != 0 || len(runner.calls) != before {
		t.Fatalf("a second restart: code = %d, calls %q, want nothing left to start", code, runner.calls[before:])
	}
	if schedulerIsLaunchd {
		return
	}
	failing := &layoutTestRunner{failStart: true, states: map[string]string{nameSyncTimerUnit: "inactive"}}
	journal = newJournal(failing)
	journal.holdServices(scheduler, true)
	if code := journal.RestartSchedulerUnits(0, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "pfm install: restart name-sync scheduler") {
		t.Fatalf("failed start: code = %d stderr=%q, want 1 and the restart failure", code, stderr.String())
	}
}
