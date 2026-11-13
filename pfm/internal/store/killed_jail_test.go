package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// A grown clear-kill that cannot be expired, because the delete fails or
// another writer keeps changing the row, stays killed and is warned: the read
// itself never fails.
func TestPromptBaselineKillThatCannotExpireStaysKilled(t *testing.T) {
	for _, test := range []struct {
		name    string
		trigger string
		warning string
	}{
		{
			name:    "delete refused",
			trigger: "SELECT RAISE(FAIL,'unkill refused')",
			warning: "WARNING: pfm could not lift the expired clear-kill of \"clear-killed\" in {state}; " +
				"it stays killed: expire shared clear kill \"clear-killed\": constraint failed: unkill refused (1811)\n",
		},
		{
			name:    "row changes on every pass",
			trigger: "SELECT RAISE(IGNORE)",
			warning: "WARNING: pfm's clear-kill state in {state} changed during each of 3 auto-unkill passes; " +
				"the chats whose kill changed stay killed until the next read\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			setStoreTestJail(t)
			var warnings strings.Builder
			database := openFileTestStore(t, WithWarningWriter(&warnings))
			t.Cleanup(func() { _ = database.Close() })
			ctx := context.Background()
			const id = "clear-killed"
			baseline := int64(2)
			if err := database.Kill(ctx, Killed{
				ID: id, Engine: pfmengine.Claude, KilledAt: 10, BaselinePrompts: &baseline,
			}); err != nil {
				t.Fatal(err)
			}
			if err := database.UpsertTranscript(ctx, Transcript{
				UUID: id, Path: "/claude/clear-killed.jsonl", PromptCount: 3,
			}); err != nil {
				t.Fatal(err)
			}
			state, err := sqlitedb.OpenReadWrite(database.state.Path(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_, err = state.ExecContext(
				ctx,
				"CREATE TRIGGER hold_kill BEFORE DELETE ON hidden BEGIN "+test.trigger+"; END",
			)
			if err := errors.Join(err, state.Close()); err != nil {
				t.Fatal(err)
			}
			killed, err := database.KilledChats(ctx)
			want := []Killed{{ID: id, Engine: pfmengine.Claude, KilledAt: 10, BaselinePrompts: &baseline}}
			if err != nil || !reflect.DeepEqual(killed, want) {
				t.Fatalf("killed chats = %#v, %v; want %#v", killed, err, want)
			}
			if want := strings.ReplaceAll(test.warning, "{state}", database.state.Path()); warnings.String() != want {
				t.Fatalf("warnings = %q, want %q", warnings.String(), want)
			}
		})
	}
}

// A persistent busy database must be loud and nonzero; success would claim an
// operator decision was durable when no row was written.
func TestKilledBusyPolicyWarnsAndRejectsTheChange(t *testing.T) {
	setStoreTestJail(t)

	var warnings bytes.Buffer
	writer := openFileTestStore(t, WithWarningWriter(&warnings))
	t.Cleanup(func() { _ = writer.Close() })
	ctx := context.Background()

	if err := writer.Kill(ctx, Killed{ID: "busy-unkill", KilledAt: 2}); err != nil {
		t.Fatalf("seed Kill() error = %v", err)
	}
	if err := writer.state.SetBusyTimeout(ctx, 1); err != nil {
		t.Fatalf("shorten shared busy timeout: %v", err)
	}
	release := holdSharedWriteLock(t, writer.SharedPath())

	if err := writer.Kill(ctx, Killed{ID: "busy-kill", KilledAt: 1}); err == nil {
		t.Fatal("Kill() under persistent SQLITE_BUSY reported success")
	}
	if got := warnings.String(); !strings.Contains(got, "WARNING:") ||
		!strings.Contains(got, "was NOT") {
		t.Fatalf("busy warning = %q, want a loud not-written warning", got)
	}
	if _, found, err := writer.Killed(ctx, "busy-kill"); err != nil || found {
		t.Fatalf("Killed() after rejected kill found=%v err=%v", found, err)
	}

	warnings.Reset()
	if _, err := writer.Unkill(ctx, "busy-unkill"); err == nil {
		t.Fatal("Unkill() under persistent SQLITE_BUSY reported success")
	}
	if got := warnings.String(); !strings.Contains(got, "WARNING:") ||
		!strings.Contains(got, "was NOT") {
		t.Fatalf("busy warning = %q, want a loud not-written warning", got)
	}
	// The delete never reached the row, so the chat is still killed. Reporting
	// otherwise would be the drift this store exists to end.
	if _, found, err := writer.Killed(ctx, "busy-unkill"); err != nil || !found {
		t.Fatalf("Killed() after a busy unkill found = %v, error = %v; want true, nil", found, err)
	}
	release()
}

// L1-F18: the busy-warning counter write is fire-and-forget by design (it
// must never mask the real busy error), but its own failure must not be
// silent — it is logged with context, the same way the busy rejection itself
// is.
func TestKilledWriteLogsWhenTheBusyWarningCounterCannotBeWritten(t *testing.T) {
	setStoreTestJail(t)

	var warnings bytes.Buffer
	writer := openFileTestStore(t, WithWarningWriter(&warnings))
	t.Cleanup(func() { _ = writer.Close() })
	ctx := context.Background()

	if err := writer.Kill(ctx, Killed{ID: "seed", KilledAt: 1}); err != nil {
		t.Fatalf("seed Kill() error = %v", err)
	}
	if err := writer.state.SetBusyTimeout(ctx, 1); err != nil {
		t.Fatalf("shorten shared busy timeout: %v", err)
	}
	release := holdSharedWriteLock(t, writer.SharedPath())
	defer release()

	// The busy-warning counter lives in this Store's OWN local index db,
	// never the shared store the busy retry above targets — closing it here
	// fails only the counter write, not the kill/unkill path under test.
	if err := writer.db.Close(); err != nil {
		t.Fatalf("close local index db: %v", err)
	}

	if err := writer.Kill(ctx, Killed{ID: "busy-kill", KilledAt: 2}); err == nil {
		t.Fatal("Kill() under persistent SQLITE_BUSY reported success")
	}
	got := warnings.String()
	if !strings.Contains(got, "WARNING:") || !strings.Contains(got, "busy-warning counter") {
		t.Fatalf("warnings = %q, want the counter write failure named", got)
	}
}

// holdSharedWriteLock takes the shared database's write lock from a separate
// connection and keeps it until the returned function runs.
func holdSharedWriteLock(t *testing.T, path string) func() {
	t.Helper()

	blocker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open blocking handle: %v", err)
	}
	connection, err := blocker.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire blocking connection: %v", err)
	}
	if _, err := connection.ExecContext(
		context.Background(),
		"BEGIN IMMEDIATE",
	); err != nil {
		t.Fatalf("begin blocking transaction: %v", err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		_ = connection.Close()
		_ = blocker.Close()
	}
	t.Cleanup(release)
	return release
}

func TestConcurrentKilledWritesAcrossProcesses(t *testing.T) {
	dbPath := setStoreTestJail(t)
	initial := openFileTestStore(t)
	if err := initial.Close(); err != nil {
		t.Fatalf("initial Close() error = %v", err)
	}

	root := filepath.Dir(dbPath)
	gate := filepath.Join(root, "go")
	readyA := filepath.Join(root, "ready-a")
	readyB := filepath.Join(root, "ready-b")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	processes := []*helperCommand{
		newHelperCommand(ctx, "a", readyA, gate),
		newHelperCommand(ctx, "b", readyB, gate),
	}
	for _, process := range processes {
		if err := process.cmd.Start(); err != nil {
			t.Fatalf("start helper %s: %v", process.prefix, err)
		}
		process := process
		t.Cleanup(func() {
			if process.cmd.Process != nil {
				_ = process.cmd.Process.Kill()
			}
		})
	}

	if err := waitForPaths(ctx, readyA, readyB); err != nil {
		t.Fatalf(
			"wait for helpers: %v\n%s\n%s",
			err,
			processes[0].output.String(),
			processes[1].output.String(),
		)
	}
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatalf("release helper gate: %v", err)
	}

	for _, process := range processes {
		if err := process.cmd.Wait(); err != nil {
			t.Fatalf("helper %s: %v\n%s", process.prefix, err, process.output.String())
		}
	}

	store := openFileTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	killedChats, err := store.KilledChats(context.Background())
	if err != nil {
		t.Fatalf("KilledChats() error = %v", err)
	}
	if got, want := len(killedChats), len(processes)*helperWriteCount; got != want {
		t.Fatalf("concurrent killed write count = %d, want %d", got, want)
	}

	seen := make(map[string]bool, len(killedChats))
	for _, killed := range killedChats {
		seen[killed.ID] = true
	}
	for _, prefix := range []string{"a", "b"} {
		for index := 0; index < helperWriteCount; index++ {
			id := fmt.Sprintf("%s-%03d", prefix, index)
			if !seen[id] {
				t.Fatalf("concurrent killed writes lost %q", id)
			}
		}
	}
}

type helperCommand struct {
	cmd    *exec.Cmd
	prefix string
	output bytes.Buffer
}

func newHelperCommand(
	ctx context.Context,
	prefix string,
	ready string,
	gate string,
) *helperCommand {
	helper := &helperCommand{prefix: prefix}
	helper.cmd = exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestKilledWriteHelperProcess$",
	)
	helper.cmd.Env = append(
		os.Environ(),
		helperProcessEnv+"=1",
		helperPrefixEnv+"="+prefix,
		helperReadyEnv+"="+ready,
		helperGateEnv+"="+gate,
	)
	helper.cmd.Stdout = &helper.output
	helper.cmd.Stderr = &helper.output
	return helper
}
