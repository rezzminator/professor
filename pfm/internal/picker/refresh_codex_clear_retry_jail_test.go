//go:build jail

package picker

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gather"
	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

func TestCodexClearRefreshesBaselineAndRetainsFailedRetirement(t *testing.T) {
	testjail.TmuxServer(t)
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
				faultDB, err = sqlitedb.OpenReadWrite(database.SharedPath(), 5*time.Second)
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
	testjail.TmuxServer(t)
	runParkedCodexClear(t, false)
}

func TestParkedPickerRetriesRefreshAfterCodexClear(t *testing.T) {
	testjail.TmuxServer(t)
	runParkedCodexClear(t, true)
}

// TestParkedPickerRetriesWarnedBindingFailureWithUnchangedHeldRollout pins
// the retry boundary for the parked identity probe. A live process can keep
// the same rollout open while the first attempt to retire the old binding
// fails; that warning must invalidate the fingerprint cache so the next
// probe retries the database write instead of treating the unchanged rollout
// as proof that reconciliation succeeded.
func TestParkedPickerRetriesWarnedBindingFailureWithUnchangedHeldRollout(t *testing.T) {
	testjail.TmuxServer(t)
	database, manager, socket, oldID, currentID := codexRegatherJailFixture(t)
	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	const warning = "record clear kill"
	var warnings atomic.Int64
	stream := startParkedStream(t, ctx, database, func(stderr *bytes.Buffer) fleet.Warn {
		return func(message string) {
			fleet.PrintWarn(stderr)(message)
			if strings.Contains(message, warning) {
				warnings.Add(1)
			}
		}
	}, refreshDependencies{})

	// The stream's first pass normally advances oldID to the status-line
	// identity. Put the binding back so the next parked probe has one clear to
	// retire, then give the fake live process a stable held current rollout.
	if _, err := manager.Unkill(ctx, oldID); err != nil {
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

	faultDB, err := sqlitedb.OpenReadWrite(database.SharedPath(), 5*time.Second)
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

	// Each probe that fails to retire the binding warns: the fingerprint cache
	// must not hold, so the next probe tries the write again. A cache that held
	// would stop the probes after their first warning, leaving the count at the
	// few the passes' own attempts make; a probe that retries keeps it climbing.
	const retries = 8
	stream.await(t, "the parked probe to retry the binding failure until its warning is the 8th",
		func(ui.Snapshot) {}, func() bool { return warnings.Load() >= retries })
}

func runParkedCodexClear(t *testing.T, failRefresh bool) {
	t.Helper()
	database, manager, socket, oldID, currentID := codexRegatherJailFixture(t)
	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	if _, _, err := manager.AdvanceCodexPane(ctx, socket, "%0", currentID); err != nil {
		t.Fatal(err)
	}
	resolved := jailPaths(t)
	dependencies := refreshDependencies{}
	if failRefresh {
		dependencies.newIndexer = func(*store.Store) (indexRunner, error) { return &clearRetryIndexRunner{}, nil }
	}
	stream := startParkedStream(t, ctx, database, func(stderr *bytes.Buffer) fleet.Warn {
		return fleet.PrintWarn(stderr)
	}, dependencies)
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
	// The expensive Codex identity probe has its own cadence, separate from
	// the cheap parked presence poll: the stream's polls pass until it runs.
	cleared := false
	stream.await(t, "the parked picker to publish the Codex /clear", func(snapshot ui.Snapshot) {
		// A failed pass publishes a snapshot with no fleet (the injected
		// index failure above): it is the stale marker, not a look at the rows.
		if snapshot.Refreshing || snapshot.RefreshFailure != nil {
			return
		}
		for index := range snapshot.Rows {
			if snapshot.Rows[index].ID == currentID {
				// A pre-clear snapshot may already be queued on the channel.
				return
			}
		}
		assertNoStaleCodexRow(t, snapshot.Rows, currentID, oldID, "parked clear")
		cleared = true
	}, func() bool { return cleared })
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

func codexJailRollout(t *testing.T, database *store.Store, root, id string, promptCount int64) string {
	t.Helper()
	rolloutPath := filepath.Join(
		root,
		"codex",
		"sessions",
		"2030",
		"01",
		"02",
		"rollout-2030-01-02T03-04-05-"+id+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session_meta","payload":{"id":"` + id + `","thread_source":"user","cwd":"/work/example"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(
		context.Background(),
		store.Rollout{ID: id, Path: rolloutPath, CWD: "/work/example", UserThread: true, PromptCount: promptCount},
	); err != nil {
		t.Fatal(err)
	}
	return rolloutPath
}

func codexPane(socket, paneID string) gather.ProbePane {
	return gather.ProbePane{
		Socket:         socket,
		SessionName:    socket,
		WindowID:       "@1",
		PaneID:         paneID,
		CurrentCommand: "codex",
	}
}
