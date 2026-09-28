package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// layoutRollbackJournal journals a state-db move and, with legacyConfig, the
// move of the legacy config into the clone; it returns the journal id and the
// legacy config path.
func layoutRollbackJournal(t *testing.T, env LayoutEnv, legacyConfig bool) (string, string) {
	t.Helper()
	journal := &Journal{env: env}
	legacy := filepath.Join(env.LegacyConfigDir, pfmconfig.FileName)
	if legacyConfig {
		if err := os.Remove(env.ConfigPath); err != nil {
			t.Fatal(err)
		}
		layoutWrite(t, legacy, `{"version":2}`)
		if err := journal.mutate(
			LayoutFinding{Row: layoutRowConfig, Verdict: VerdictMove, Source: legacy, Path: env.ConfigPath},
			[]string{legacy, env.ConfigPath},
			func() error { return os.Rename(legacy, env.ConfigPath) },
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.mutate(
		LayoutFinding{Row: layoutRowStateDB, Verdict: VerdictMove, Path: env.StateDB},
		[]string{env.StateDB},
		func() error { return os.WriteFile(env.StateDB, []byte("moved"), 0o600) },
	); err != nil {
		t.Fatal(err)
	}
	return filepath.Base(journal.dir), legacy
}

// layoutNextBlock is the ordered next step a rollback onto a legacy config
// prints (0-update-window.md).
func layoutNextBlock(env LayoutEnv, legacy string, withMake, daemonReload bool) string {
	block := fmt.Sprintf("  next    this pfm refuses the restored legacy config %s; fleet units left stopped: %s\n",
		legacy, layoutStoppedList())
	var steps []string
	if daemonReload {
		steps = append(steps, "systemctl --user daemon-reload")
	}
	if withMake {
		steps = append(steps, "make -C "+filepath.Join(env.Clone, "pfm")+" rollback")
	}
	if schedulerIsLaunchd {
		for _, label := range []string{mcpLaunchdLabel, launchdLabel} {
			steps = append(steps, fmt.Sprintf("launchctl bootstrap gui/%d %s", os.Getuid(),
				filepath.Join(env.Home, "Library", "LaunchAgents", label+".plist")))
		}
	} else {
		steps = append(steps, "systemctl --user start "+fleetUnitList)
	}
	for index, step := range steps {
		block += fmt.Sprintf("  next    %d. %s\n", index+1, step)
	}
	return block
}

func layoutStoppedList() string {
	if schedulerIsLaunchd {
		return mcpLaunchdLabel + " " + launchdLabel
	}
	return fleetUnitList
}

func layoutRestartVerbs() (string, string) {
	if schedulerIsLaunchd {
		return " bootout ", " bootstrap "
	}
	return " stop ", " start "
}

func TestLayoutRollbackLeavesFleetStoppedOnRestoredLegacyConfig(t *testing.T) {
	env := layoutFixture(t)
	id, legacy := layoutRollbackJournal(t, env, true)
	runner := &layoutTestRunner{}
	env.runner = runner
	var output bytes.Buffer
	if err := RollbackLayout(context.Background(), env, id, false, &output); err != nil {
		t.Fatalf("rollback: %v output=%s", err, output.String())
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy config not restored: %v", err)
	}
	stop, start := layoutRestartVerbs()
	if layoutCountCalls(runner.calls, stop) == 0 || layoutCountCalls(runner.calls, start) != 0 {
		t.Fatalf("fleet restarted on a refused config: %q", runner.calls)
	}
	want := layoutNextBlock(env, legacy, true, false)
	if !strings.HasSuffix(output.String(), want) {
		t.Fatalf("output =\n%s\nwant suffix\n%s", output.String(), want)
	}
}

// TestLayoutRollbackOfConfigAloneStopsFleet: the config moves at the state-db
// row and is journaled even when that row then refuses (a picker held the
// database), so a journal can hold the config move and no database record.
// Its rollback restores the legacy config: the running units, whose binary
// refuses it, stop and stay stopped.
func TestLayoutRollbackOfConfigAloneStopsFleet(t *testing.T) {
	env := layoutFixture(t)
	legacy := filepath.Join(env.LegacyConfigDir, pfmconfig.FileName)
	if err := os.Remove(env.ConfigPath); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, legacy, `{"version":2}`)
	journal := &Journal{env: env}
	if err := journal.mutate(
		LayoutFinding{Row: layoutRowConfig, Verdict: VerdictMove, Source: legacy, Path: env.ConfigPath},
		[]string{legacy, env.ConfigPath},
		func() error { return os.Rename(legacy, env.ConfigPath) },
	); err != nil {
		t.Fatal(err)
	}
	runner := &layoutTestRunner{}
	env.runner = runner
	var output bytes.Buffer
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), false, &output); err != nil {
		t.Fatalf("rollback: %v output=%s", err, output.String())
	}
	stop, start := layoutRestartVerbs()
	if layoutCountCalls(runner.calls, stop) == 0 || layoutCountCalls(runner.calls, start) != 0 {
		t.Fatalf("fleet left running on a refused config: %q", runner.calls)
	}
	if want := layoutNextBlock(env, legacy, true, false); !strings.HasSuffix(output.String(), want) {
		t.Fatalf("output =\n%s\nwant suffix\n%s", output.String(), want)
	}
}

func TestLayoutRollbackOnMigratedConfigRestartsAndVerifies(t *testing.T) {
	for _, afterStart := range []string{"active", "failed"} {
		t.Run(afterStart, func(t *testing.T) {
			if schedulerIsLaunchd {
				return
			}
			env := layoutFixture(t)
			id, _ := layoutRollbackJournal(t, env, false)
			runner := &layoutTestRunner{afterStart: map[string]string{mcpUnitName: afterStart}}
			env.runner = runner
			var output bytes.Buffer
			err := RollbackLayout(context.Background(), env, id, false, &output)
			if afterStart == "active" && err != nil {
				t.Fatal(err)
			}
			want := "fleet unit pfm-mcp.service is failed 3s after start"
			if afterStart == "failed" && (err == nil || !strings.Contains(err.Error(), want)) {
				t.Fatalf("rollback error = %v, want %q", err, want)
			}
			if layoutCountCalls(runner.calls, " stop ") != 1 || layoutCountCalls(runner.calls, " start ") != 1 {
				t.Fatalf("lifecycle = %q", runner.calls)
			}
			calls := layoutSystemctlCalls(runner.calls)
			verify := calls[len(calls)-len(layoutServiceUnits):]
			if strings.Join(verify, "\n") != strings.Join(layoutProbeCalls(), "\n") {
				t.Fatalf("verify probes = %q", verify)
			}
			if strings.Contains(output.String(), "  next ") {
				t.Fatalf("next step printed on a migrated config: %s", output.String())
			}
		})
	}
}

func TestLayoutRollbackUnderUpdaterOmitsMakeRollback(t *testing.T) {
	env := layoutFixture(t)
	env.invocation = &paths.MapEnv{Values: map[string]string{paths.EnvUpdateInstall: "1"}}
	id, legacy := layoutRollbackJournal(t, env, true)
	runner := &layoutTestRunner{}
	env.runner = runner
	var output bytes.Buffer
	if err := RollbackLayout(context.Background(), env, id, false, &output); err != nil {
		t.Fatal(err)
	}
	_, start := layoutRestartVerbs()
	if layoutCountCalls(runner.calls, start) != 0 {
		t.Fatalf("fleet restarted under the updater: %q", runner.calls)
	}
	if want := layoutNextBlock(env, legacy, false, false); !strings.HasSuffix(output.String(), want) {
		t.Fatalf("output =\n%s\nwant suffix\n%s", output.String(), want)
	}
}

func TestLayoutRollbackFailedReplayOnLegacyConfigLeavesFleetStopped(t *testing.T) {
	env := layoutFixture(t)
	id, _ := layoutRollbackJournal(t, env, true)
	dir := filepath.Join(env.Home, ".local", "state", "pfm", "migrations", id)
	journal := &Journal{env: env, dir: dir}
	journal.records = readLayoutJournalRecords(t, dir)
	journal.records = append(journal.records, layoutJournalRecord{
		Row:         layoutRowInstall,
		Destination: filepath.Join(t.TempDir(), "operator.txt"),
		Result:      layoutRecordApplied,
	})
	if err := journal.flush(); err != nil {
		t.Fatal(err)
	}
	runner := &layoutTestRunner{}
	env.runner = runner
	var output bytes.Buffer
	err := RollbackLayout(context.Background(), env, id, false, &output)
	want := "fleet units left stopped: " + layoutStoppedList() + " — this pfm refuses the restored legacy config"
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("rollback error = %v, want %q", err, want)
	}
	_, start := layoutRestartVerbs()
	if layoutCountCalls(runner.calls, start) != 0 {
		t.Fatalf("fleet restarted on a refused config: %q", runner.calls)
	}
	if strings.Contains(output.String(), "  next ") {
		t.Fatalf("next step printed after a failed replay: %s", output.String())
	}
}

func TestLayoutRollbackDaemonReloadJoinsNextBlock(t *testing.T) {
	env := layoutFixture(t)
	unit := filepath.Join(env.Home, ".config", "systemd", "user", mcpUnitName)
	layoutWrite(t, unit, "[Unit]\n")
	journal := &Journal{env: env}
	if err := journal.before(unit); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(env.LegacyConfigDir, pfmconfig.FileName)
	if err := os.Remove(env.ConfigPath); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, legacy, `{"version":2}`)
	if err := journal.mutate(
		LayoutFinding{Row: layoutRowConfig, Verdict: VerdictMove, Source: legacy, Path: env.ConfigPath},
		[]string{legacy, env.ConfigPath},
		func() error { return os.Rename(legacy, env.ConfigPath) },
	); err != nil {
		t.Fatal(err)
	}
	if err := journal.mutate(
		LayoutFinding{Row: layoutRowStateDB, Verdict: VerdictMove, Path: env.StateDB},
		[]string{env.StateDB},
		func() error { return os.WriteFile(env.StateDB, []byte("moved"), 0o600) },
	); err != nil {
		t.Fatal(err)
	}
	env.runner = &layoutTestRunner{}
	var output bytes.Buffer
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), false, &output); err != nil {
		t.Fatalf("rollback: %v output=%s", err, output.String())
	}
	if want := layoutNextBlock(env, legacy, true, true); !strings.HasSuffix(output.String(), want) {
		t.Fatalf("output =\n%s\nwant suffix\n%s", output.String(), want)
	}
	if strings.Contains(output.String(), systemdDaemonReloadNote) {
		t.Fatalf("standalone daemon-reload note printed beside the next block: %s", output.String())
	}
}

func readLayoutJournalRecords(t *testing.T, dir string) []layoutJournalRecord {
	t.Helper()
	var records []layoutJournalRecord
	raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	return records
}
