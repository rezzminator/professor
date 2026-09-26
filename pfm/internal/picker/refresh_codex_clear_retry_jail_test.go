package picker

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gather"
	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

func TestCodexClearRefreshesBaselineAndRetainsFailedRetirement(t *testing.T) {
	for _, scenario := range []string{"unindexed", "missing-rollout", "write-failed", "stale-baseline"} {
		t.Run(scenario, func(t *testing.T) {
			root := jailTest(t)
			ctx := context.Background()
			tmuxTmpDir := filepath.Join(root, "tmuxtmp")
			const socket = "cx-1800000901-1-1"
			const oldID = "11111111-1111-4111-8111-111111111111"
			const newID = "22222222-2222-4222-8222-222222222222"
			startCodexStatusPane(t, tmuxTmpDir, socket, "  "+newID+` · /work/example · Full Access\n`)
			resolved := jailPaths(t)
			resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
			database, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := database.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			}()
			manager, err := kill.New(database, kill.Dependencies{})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", oldID); err != nil {
				t.Fatal(err)
			}
			var rolloutPath string
			if scenario != "unindexed" {
				rolloutPath = codexJailRollout(t, database, root, oldID, 1)
			}
			if scenario == "missing-rollout" {
				if err := os.Remove(rolloutPath); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "stale-baseline" {
				appendCodexClearPrompt(t, rolloutPath)
			}
			var faultDB *sql.DB
			if scenario == "write-failed" {
				faultDB, err = sql.Open("sqlite", database.SharedPath())
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := faultDB.Close(); err != nil {
						t.Errorf("close faultDB: %v", err)
					}
				}()
				if _, err := faultDB.Exec(
					`CREATE TRIGGER reject_clear BEFORE INSERT ON hidden BEGIN SELECT RAISE(FAIL, 'clear write fault'); END`,
				); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			reconcile := func() {
				fleet.ReconcileCodexPanes(
					ctx,
					database,
					gather.Snapshot{Panes: []gather.ProbePane{codexPane(socket, "%0")}},
					pfmconfig.Runtime{Paths: resolved},
					fleet.PrintWarn(&stderr),
				)
			}
			reconcile()
			if scenario != "stale-baseline" {
				bound, found, err := manager.CodexPaneBinding(ctx, socket, "%0")
				if err != nil || !found || bound != oldID {
					t.Fatalf(
						"failed retirement lost retry: binding=%q found=%v err=%v warnings=%s",
						bound,
						found,
						err,
						stderr.String(),
					)
				}
				if stderr.Len() == 0 {
					t.Fatal("failed retirement was silent")
				}
				if faultDB != nil {
					if _, err := faultDB.Exec(`DROP TRIGGER reject_clear`); err != nil {
						t.Fatal(err)
					}
				}
				rolloutPath = codexJailRollout(t, database, root, oldID, 1)
				reconcile()
			}
			indexer, err := fleetindex.NewWithPaths(database, resolved)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := indexer.Run(ctx, fleetindex.Options{}); err != nil {
				t.Fatal(err)
			}
			lineage, found, err := database.CodexLineage(ctx, oldID)
			if err != nil || !found {
				t.Fatalf("lineage missing: %v", err)
			}
			killed, found, err := database.Killed(ctx, oldID)
			if err != nil || !found || killed.BaselinePrompts == nil || *killed.BaselinePrompts != lineage.PromptCount {
				t.Fatalf(
					"index catch-up undid clear: kill=%#v prompts=%d found=%v err=%v warnings=%s",
					killed,
					lineage.PromptCount,
					found,
					err,
					stderr.String(),
				)
			}
			bound, found, err := manager.CodexPaneBinding(ctx, socket, "%0")
			if err != nil || !found || bound != newID {
				t.Fatalf("successful retirement did not advance: %q %v %v", bound, found, err)
			}
			// An actual later prompt still lifts the temporary clear hide.
			appendCodexClearPrompt(t, rolloutPath)
			if _, err := indexer.Run(ctx, fleetindex.Options{}); err != nil {
				t.Fatal(err)
			}
			if _, found, err := database.Killed(ctx, oldID); err != nil || found {
				t.Fatalf("genuine resumed prompt did not lift clear hide: found=%v err=%v", found, err)
			}
		})
	}
}

func TestParkedPickerStillObservesCodexClear(t *testing.T) {
	runParkedCodexClear(t, false)
}

func TestParkedPickerRetriesRefreshAfterCodexClear(t *testing.T) {
	runParkedCodexClear(t, true)
}

// TestParkedPickerRetriesWarnedBindingFailureWithUnchangedHeldRollout pins
// the retry boundary for the parked identity probe. A live process can keep
// the same rollout open while the first attempt to retire the old binding
// fails; that warning must invalidate the fingerprint cache so the next
// probe retries the database write instead of treating the unchanged rollout
// as proof that reconciliation succeeded.
func TestParkedPickerRetriesWarnedBindingFailureWithUnchangedHeldRollout(t *testing.T) {
	shortenRefreshIntervals(t)
	database, manager, socket, oldID, currentID := codexRegatherJailFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan ui.Snapshot, 8)
	var stderr bytes.Buffer
	const warning = "record clear kill"
	warningEvents := make(chan string, 8)
	warn := func(message string) {
		fleet.PrintWarn(&stderr)(message)
		if strings.Contains(message, warning) {
			warningEvents <- message
		}
	}
	go streamFleetRefreshesWith(
		ctx,
		database,
		scanRequest{},
		warn,
		&stderr,
		updates,
		refreshDependencies{activity: ui.NewActivityClock(time.Now())},
	)
	defer func() {
		cancel()
		for range updates {
		}
	}()

	for completed := 0; completed < 2; {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatalf("stream closed before park: %s", stderr.String())
			}
			if !snapshot.Refreshing {
				completed++
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("stream did not reach park: %s", stderr.String())
		}
	}

	// The stream's first pass normally advances oldID to the status-line
	// identity. Put the binding back so the next parked probe has one clear to
	// retire, then give the fake live process a stable held current rollout.
	if err := manager.Unkill(ctx, oldID); err != nil {
		t.Fatalf("remove first-pass clear retirement: %v", err)
	}
	if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", oldID); err != nil {
		t.Fatal(err)
	}
	current, found, err := database.Rollout(ctx, currentID)
	if err != nil || !found {
		t.Fatalf("current rollout missing: %v", err)
	}
	procRoot := jailPaths(t).ProcRoot
	fdPath := filepath.Join(procRoot, "90301", "fd", "3")
	if err := os.Symlink(current.Path, fdPath); err != nil {
		t.Fatalf("hold current rollout in fake Codex process: %v", err)
	}

	faultDB, err := sql.Open("sqlite", database.SharedPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := faultDB.Close(); err != nil {
			t.Errorf("close faultDB: %v", err)
		}
	}()
	if _, err := faultDB.Exec(
		`CREATE TRIGGER reject_clear_retry BEFORE INSERT ON hidden BEGIN SELECT RAISE(FAIL, 'clear retry fault'); END`,
	); err != nil {
		t.Fatal(err)
	}

	bound := 2*fleetRefreshCodexPollInterval + fleetRefreshParkPollInterval + 5*time.Second
	deadline := time.After(bound)
	for attempts := 0; attempts < 2; attempts++ {
		select {
		case <-warningEvents:
			continue
		case _, ok := <-updates:
			if !ok {
				t.Fatalf("stream closed before the second binding warning: %s", stderr.String())
			}
		case <-deadline:
			t.Fatalf(
				"parked probe retried binding failure fewer than twice within %s: warnings=%q",
				bound,
				stderr.String(),
			)
		}
	}
}

type clearRetryIndexRunner struct{ calls int }

func (runner *clearRetryIndexRunner) Run(context.Context, fleetindex.Options) (fleetindex.Counters, error) {
	runner.calls++
	if runner.calls == 3 {
		return fleetindex.Counters{}, fmt.Errorf("injected post-clear index failure")
	}
	return fleetindex.Counters{}, nil
}

func runParkedCodexClear(t *testing.T, failRefresh bool) {
	t.Helper()
	shortenRefreshIntervals(t)
	database, manager, socket, oldID, currentID := codexRegatherJailFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", currentID); err != nil {
		t.Fatal(err)
	}
	resolved := jailPaths(t)
	updates := make(chan ui.Snapshot, 8)
	var stderr bytes.Buffer
	dependencies := refreshDependencies{activity: ui.NewActivityClock(time.Now())}
	if failRefresh {
		dependencies.newIndexer = func(*store.Store) (indexRunner, error) { return &clearRetryIndexRunner{}, nil }
	}
	go streamFleetRefreshesWith(ctx, database, scanRequest{}, fleet.PrintWarn(&stderr), &stderr, updates, dependencies)
	defer func() {
		cancel()
		for range updates {
		}
	}()
	for completed := 0; completed < 2; {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatal("stream closed before park")
			}
			if !snapshot.Refreshing {
				completed++
			}
		case <-time.After(15 * time.Second):
			t.Fatal("stream did not reach park")
		}
	}
	current, found, err := database.Rollout(ctx, currentID)
	if err != nil || !found {
		t.Fatalf("current rollout missing: %v", err)
	}
	appendCodexClearPrompt(t, current.Path)
	// Use the fixture's other indexed thread as the new bare identity. This
	// exercises real tmux capture without touching the picker's activity clock.
	command := exec.Command(
		"tmux",
		"-L",
		socket,
		"send-keys",
		"-t",
		"%0",
		"-l",
		"\n  "+oldID+" · /work/example · Full Access",
	)
	command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+filepath.Dir(resolved.TmuxDir))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("change fixture identity: %v: %s", err, output)
	}
	// The expensive Codex identity probe has its own 10s cadence, separate
	// from the cheap 2s parked presence poll. Give the probe one full interval
	// plus headroom, rather than timing this test against the presence timer.
	bound := fleetRefreshCodexPollInterval + 15*time.Second
	deadline := time.After(bound)
	for {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatal("stream closed after clear")
			}
			if snapshot.Refreshing {
				continue
			}
			for index := range snapshot.Rows {
				row := &snapshot.Rows[index]
				if row.ID == currentID {
					t.Fatalf("cleared chat remained visible: %#v", row)
				}
			}
			assertNoStaleCodexRow(t, snapshot.Rows, currentID, oldID, "parked clear")
			return
		case <-deadline:
			t.Fatalf("parked picker missed Codex /clear within %s", bound)
		}
	}
}

func appendCodexClearPrompt(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}}` + "\n",
	)
	closeErr := file.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}
