package fleetdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestClearNameDurableAndErrorsVisible(t *testing.T) {
	db, _ := openTestStore(t)
	ctx := context.Background()
	const id = "22222222-2222-4222-8222-222222222222"
	fresh, err := naming.ClearSuccessorName("Desk", id)
	if err != nil {
		t.Fatal(err)
	}
	name := naming.ClearName{Inherited: "Desk", Name: fresh}
	if err := db.PutClearName(ctx, id, name, 1); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.ClearName(ctx, id)
	if err != nil || !found || got != name {
		t.Fatalf("ClearName=%+v,%v,%v", got, found, err)
	}
	name.Emitted = true
	if err := db.PutClearName(ctx, id, name, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta(ctx, clearNamePrefix+id, "broken-json", 3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClearName(ctx, id); err == nil {
		t.Fatal("corrupt record rendered as absence")
	}
	degraded := &Store{degraded: context.Canceled}
	if err := degraded.PutClearName(ctx, id, name, 4); err == nil {
		t.Fatal("failed write rendered as success")
	}
	if _, _, err := degraded.ClearName(ctx, id); err == nil {
		t.Fatal("failed read rendered as absence")
	}
}

func TestClearNameObservationsMergeStaleWriters(t *testing.T) {
	for _, confirmFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "prompt first", true: "observer first"}[confirmFirst], func(t *testing.T) {
			db, _ := openTestStore(t)
			ctx := context.Background()
			const id = "22222222-2222-4222-8222-222222222222"
			initial := naming.ClearName{Inherited: "Desk", Name: "Desk #2"}
			if err := db.PutClearName(ctx, id, initial, 1); err != nil {
				t.Fatal(err)
			}
			// Both actors read before either writes: the prompt marks output,
			// while an index/reporter observation confirms the engine title.
			prompt := initial
			prompt.Emitted = true
			observer := initial.ObserveTitle(initial.Name)
			updates := []naming.ClearName{prompt, observer}
			if confirmFirst {
				updates[0], updates[1] = updates[1], updates[0]
			}
			for _, update := range updates {
				if err := db.PutClearName(ctx, id, update, 2); err != nil {
					t.Fatal(err)
				}
			}
			got, found, err := db.ClearName(ctx, id)
			if err != nil || !found || !got.Emitted || !got.Confirmed || got.Title("Desk") != "Desk" {
				t.Fatalf("stale writer lost a naming observation: %+v found=%v err=%v", got, found, err)
			}
		})
	}
}

func TestReserveClearNameCountsFleetLabelsAndRollsBackWriteFailure(t *testing.T) {
	state, _ := openTestStore(t)
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
