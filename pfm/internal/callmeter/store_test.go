package callmeter

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenDB(context.Background(), filepath.Join(t.TempDir(), "state", "callmeter.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

// row reads one row of table as column name -> value (nil for NULL).
func row(t *testing.T, store *Store, table, where string, args ...any) map[string]any {
	t.Helper()
	rows, err := store.DB().Query(fmt.Sprintf("SELECT * FROM %s WHERE %s", table, where), args...)
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close %s rows: %v", table, err)
		}
	}()
	names, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		return nil
	}
	values := make([]any, len(names))
	pointers := make([]any, len(names))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		t.Fatalf("scan %s: %v", table, err)
	}
	out := map[string]any{}
	for i, name := range names {
		out[name] = values[i]
	}
	return out
}

func count(t *testing.T, store *Store, table string) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestDefaultPath(t *testing.T) {
	if got, want := DefaultPath("/tmp/demo-home"), "/tmp/demo-home/.local/state/pfm/callmeter.db"; got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

func TestOpenSetsPragmasAndVersion(t *testing.T) {
	store := openTestStore(t)
	for pragma, want := range map[string]string{
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"foreign_keys": "0",
		"user_version": "1",
	} {
		var got string
		if err := store.DB().QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "callmeter.db")
	store, err := OpenDB(ctx, path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if _, err := store.DB().Exec("PRAGMA user_version=2"); err != nil {
		t.Fatalf("raise version: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := OpenDB(ctx, path)
	if err == nil {
		_ = reopened.Close()
		t.Fatal("OpenDB of a version-2 store succeeded, want a refusal")
	}
	for _, want := range []string{"version 2", "version 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	// The refusal left the store as it was: a newer binary still reads it.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	}()
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 2 {
		t.Fatalf("user_version after refusal = %d, want 2", version)
	}
}

func TestPruneByAge(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old, recent := cutoff.Add(-time.Hour).UnixMilli(), cutoff.Add(time.Hour).UnixMilli()
	for id, ts := range map[string]int64{"toolu_old": old, "toolu_new": recent} {
		if err := store.UpsertCall(ctx, Call{ToolUseID: id, TS: Ptr(ts)}, Overwrite); err != nil {
			t.Fatal(err)
		}
		if err := store.ReplaceCommandParts(ctx, id, []CommandPart{{Seq: 0, Lang: "sh", Program: "cat"}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertRequest(ctx, Request{RequestID: "msg_" + id, TS: Ptr(ts)}, Overwrite); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAgent(ctx, Agent{AgentID: "agent_" + id, Started: Ptr(ts)}, Overwrite); err != nil {
			t.Fatal(err)
		}
		if err := store.AddFault(ctx, Fault{TS: ts, ToolUseID: id, Stage: StageParse, Error: "boom"}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.Prune(ctx, cutoff)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 5 {
		t.Errorf("Prune removed %d rows, want 5 (one per table)", removed)
	}
	for _, table := range []string{"calls", "requests", "agents", "faults", "command_parts"} {
		if n := count(t, store, table); n != 1 {
			t.Errorf("%s holds %d rows after prune, want 1", table, n)
		}
	}
	if row(t, store, "calls", "tool_use_id = ?", "toolu_new") == nil {
		t.Error("the recent call was pruned")
	}
	if row(t, store, "command_parts", "tool_use_id = ?", "toolu_new") == nil {
		t.Error("the recent call's command parts were pruned")
	}
}

func TestAddFaultStoresEmptyAsNull(t *testing.T) {
	store := openTestStore(t)
	if err := store.AddFault(context.Background(), Fault{TS: 1, Stage: StagePayload, Error: "bad json"}); err != nil {
		t.Fatal(err)
	}
	got := row(t, store, "faults", "stage = ?", StagePayload)
	if got["session_id"] != nil || got["tool_use_id"] != nil {
		t.Fatalf("empty ids stored as %v / %v, want NULL", got["session_id"], got["tool_use_id"])
	}
}
