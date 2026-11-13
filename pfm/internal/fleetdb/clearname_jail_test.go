package fleetdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestReserveClearNameCountsFleetLabelsAndRollsBackWriteFailure(t *testing.T) {
	// The file: ReserveClearName attaches the cache file by path, which the in-memory state cannot see.
	state, _ := openFileTestStore(t)
	ctx := context.Background()
	cachePath := filepath.Join(t.TempDir(), "cache.db")
	cache, err := sqlitedb.OpenStore(ctx, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cache.Close() }()
	// Only the naming columns matter to this state-store unit test.
	if _, err := cache.Exec(`CREATE TABLE transcripts(uuid TEXT, custom_title TEXT, ai_title TEXT, first_prompt TEXT);
CREATE TABLE cx_names(id TEXT, thread_name TEXT);`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.exec(ctx, "INSERT INTO chat(uuid,label) VALUES(?,?)", "predecessor", "Desk #2"); err != nil {
		t.Fatal(err)
	}
	const id = "22222222-2222-4222-8222-222222222222"
	name, err := state.ReserveClearName(ctx, id, "Desk", cachePath, 1)
	if err != nil || name.Name != "Desk #3" {
		t.Fatalf("fleet label not counted: %+v err=%v", name, err)
	}
	if got, found, err := state.ClearName(ctx, id); err != nil || !found || got != name {
		t.Fatalf("reservation not durable: %+v found=%v err=%v", got, found, err)
	}
	if _, err := state.exec(ctx, `CREATE TRIGGER refuse_clear_name BEFORE INSERT ON meta
WHEN NEW.key LIKE 'clear-name:%' BEGIN SELECT RAISE(ABORT,'fixture reservation refused'); END`); err != nil {
		t.Fatal(err)
	}
	const next = "33333333-3333-4333-8333-333333333333"
	if _, err := state.ReserveClearName(
		ctx,
		next,
		"Desk",
		cachePath,
		2,
	); err == nil ||
		!strings.Contains(err.Error(), "write clear name reservation") {
		t.Fatalf("failed write not reported: %v", err)
	}
	if got, found, err := state.ClearName(ctx, next); err != nil || found {
		t.Fatalf("failed write reserved %+v found=%v err=%v", got, found, err)
	}
	if _, err := state.exec(ctx, "DROP TRIGGER refuse_clear_name"); err != nil {
		t.Fatal(err)
	}
	name, err = state.ReserveClearName(ctx, next, "Desk", cachePath, 3)
	if err != nil || name.Name != "Desk #4" {
		t.Fatalf("write repair did not retry smallest number: %+v err=%v", name, err)
	}
	if err := state.SetMeta(ctx, clearNamePrefix+id, "broken-json", 4); err != nil {
		t.Fatal(err)
	}
	_, err = state.ReserveClearName(ctx, "44444444-4444-4444-8444-444444444444", "Desk", cachePath, 5)
	if err == nil || !strings.Contains(err.Error(), "decode clear name") {
		t.Fatalf("corrupt reservation read was not an error: %v", err)
	}
	degraded := &Store{degraded: context.Canceled}
	if _, err := degraded.ReserveClearName(ctx, next, "Desk", cachePath, 6); err == nil {
		t.Fatal("degraded state guessed a number")
	}
}
