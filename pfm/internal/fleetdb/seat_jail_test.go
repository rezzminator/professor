//go:build jail

package fleetdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestSeatConcurrentUpdates(t *testing.T) {
	state := openTestStore(t)
	ctx := context.Background()
	original := Seat{SeatID: "owner", Backend: "herdr", State: "live", Name: "old"}
	if err := state.PutSeat(ctx, original); err != nil {
		t.Fatal(err)
	}
	renamed, bound := original, original
	renamed.Name = "new"
	bound.SessionID = "new-session"
	bound.ReloadPending = true
	if err := state.UpdateSeat(ctx, original, bound); err != nil {
		t.Fatal(err)
	}
	if err := state.UpdateSeat(ctx, original, renamed); err != nil {
		t.Fatal(err)
	}
	got, found, err := state.Seat(ctx, original.SeatID)
	if err != nil || !found || got.Name != "new" || got.SessionID != "new-session" || !got.ReloadPending {
		t.Fatalf("concurrent owned fields lost: %+v found=%v err=%v", got, found, err)
	}
	if err := state.DeleteSeat(ctx, original.SeatID); err != nil {
		t.Fatal(err)
	}
	if err := state.UpdateSeat(ctx, original, renamed); err == nil {
		t.Fatal("stale update resurrected deleted seat")
	}
	if _, found, err := state.Seat(ctx, original.SeatID); err != nil || found {
		t.Fatalf("deleted seat returned: %v %v", found, err)
	}
}

func TestSeatRun(t *testing.T) {
	state, values := openFileTestStore(t)
	run := SeatRun{
		Binary:     "/bin/echo",
		Args:       []string{"hello world"},
		Env:        []string{"A=b"},
		CWD:        values.Home,
		CostLedger: true,
	}
	if err := state.WriteSeatRun("seat-1", run); err != nil {
		t.Fatal(err)
	}
	t.Run("symlink recipe rejected", func(t *testing.T) {
		dir := filepath.Join(filepath.Dir(values.StateDB), "seats")
		if err := os.Symlink(filepath.Join(dir, "seat-1.json"), filepath.Join(dir, "linked.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := state.ReadSeatRun("linked"); err == nil {
			t.Fatal("followed a symlink launch recipe")
		}
	})

	got, err := state.ReadSeatRun("seat-1")
	if err != nil || !reflect.DeepEqual(got, run) {
		t.Fatalf("run = %+v,%v", got, err)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(values.StateDB), "seats", "seat-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("run permissions = %o", info.Mode().Perm())
	}
	for _, id := range []string{"", "..", "../other", "a/b"} {
		if err := state.WriteSeatRun(id, run); err == nil {
			t.Fatalf("unsafe id %q accepted", id)
		}
	}
	if _, err := state.ReadSeatRun("missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing run = %v", err)
	}
}

func TestReadSeatsPreservesLegacyDatabase(t *testing.T) {
	state, values := openFileTestStore(t)
	if _, err := state.db.Exec("DROP TABLE seat; PRAGMA user_version=5"); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadSeats(context.Background(), values)
	if err != nil || len(rows) != 0 {
		t.Fatalf("legacy seat read=%+v %v", rows, err)
	}
	var version int
	if err := state.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 5 {
		t.Fatalf("read-only seat lookup migrated durable state: version=%d err=%v", version, err)
	}
}

func TestSeatLivePaneAtomicOwnership(t *testing.T) {
	for _, mode := range []string{"put", "bind", "revive", "stale-patch"} {
		t.Run(mode, func(t *testing.T) {
			state, values := openFileTestStore(t)
			ctx := context.Background()
			other := OpenSharedState(ctx, values)
			defer func() { _ = other.Close() }()
			stores := []*Store{state, other}
			originals := make([]Seat, 2)
			for i := range originals {
				originals[i] = Seat{
					SeatID:      fmt.Sprintf("seat-%d", i),
					Backend:     "herdr",
					State:       "live",
					HerdrSocket: "/fixture/herdr.sock",
					HerdrPane:   "w6:p3",
				}
				if mode != "put" {
					before := originals[i]
					if mode == "bind" || mode == "stale-patch" {
						before.HerdrPane = ""
					} else {
						before.State = "failed"
					}
					if err := state.PutSeat(ctx, before); err != nil {
						t.Fatal(err)
					}
					originals[i] = before
				}
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for i := range originals {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					before := originals[i]
					after := before
					after.HerdrPane = "w6:p3"
					after.State = "live"
					if mode == "put" {
						results <- stores[i].PutSeat(ctx, after)
					} else {
						results <- stores[i].UpdateSeat(ctx, before, after)
					}
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			wins := 0
			for err := range results {
				if err == nil {
					wins++
				} else if !strings.Contains(err.Error(), "already owned") {
					t.Fatalf("conflict error = %v", err)
				}
			}
			if wins != 1 {
				t.Fatalf("concurrent %s admitted %d live owners, want exactly one", mode, wins)
			}
			owner, found, err := state.SeatByPane(ctx, "/fixture/herdr.sock", "w6:p3")
			if err != nil || !found {
				t.Fatalf("owner=%+v %v %v", owner, found, err)
			}
			// A stale metadata-only patch cannot overwrite the winning address or state.
			for i, before := range originals {
				if before.SeatID == owner.SeatID && mode != "put" {
					after := before
					after.Name = "renamed"
					if err := state.UpdateSeat(ctx, before, after); err != nil {
						t.Fatalf("stale metadata patch %d: %v", i, err)
					}
				}
			}
			rows, err := state.Seats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			live := 0
			for _, row := range rows {
				if row.State == "live" && row.HerdrPane == "w6:p3" {
					live++
				}
			}
			if live != 1 {
				t.Fatalf("stored live owners=%d rows=%+v", live, rows)
			}
		})
	}
}
