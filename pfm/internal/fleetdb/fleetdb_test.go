package fleetdb

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestKillWithRevisionRollsBackOnlyAnInsertedRow(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "insert", true: "concurrent prior kill"}[existing], func(t *testing.T) {
			state := openTestStore(t)
			ctx := context.Background()
			const id = "revision-owner"
			if existing {
				if err := state.Kill(ctx, id, 42); err != nil {
					t.Fatal(err)
				}
			}
			revision, err := state.KillWithRevision(ctx, id, 42)
			if err != nil {
				t.Fatal(err)
			}
			if (revision != "") == existing {
				t.Fatalf("rollback revision %q, existing=%v; only inserts own rollback", revision, existing)
			}
			removed, err := state.UnkillIfRevision(ctx, id, revision)
			if err != nil || removed == existing {
				t.Fatalf("conditional rollback = %v, %v, existing=%v", removed, err, existing)
			}
			records, err := state.KilledRecords(ctx)
			if _, present := records[id]; err != nil || present != existing {
				t.Fatalf("kill after rollback = %v, %v, existing=%v", records, err, existing)
			}
		})
	}
}

func TestUnkillReportsDeletedRow(t *testing.T) {
	t.Parallel()
	state := openTestStore(t)
	ctx := context.Background()
	const id = "shared-unkill"
	if err := state.Kill(ctx, id, 42); err != nil {
		t.Fatal(err)
	}
	if removed, err := state.Unkill(ctx, id); err != nil || !removed {
		t.Fatalf("first Unkill() = %v, %v; want true, nil", removed, err)
	}
	if removed, err := state.Unkill(ctx, id); err != nil || removed {
		t.Fatalf("second Unkill() = %v, %v; want false, nil", removed, err)
	}
}

func TestReassertKillKeepsOriginalTimeAndMakesClearPermanent(t *testing.T) {
	t.Parallel()
	state := openTestStore(t)
	ctx := context.Background()
	const id = "shared-reassert"
	if reasserted, err := state.ReassertKill(ctx, id); err != nil || reasserted {
		t.Fatalf("missing ReassertKill() = %v, %v; want false, nil", reasserted, err)
	}
	if err := state.KillUntilPrompt(ctx, id, 42, 3); err != nil {
		t.Fatal(err)
	}
	if reasserted, err := state.ReassertKill(ctx, id); err != nil || !reasserted {
		t.Fatalf("standing ReassertKill() = %v, %v; want true, nil", reasserted, err)
	}
	records, err := state.KilledRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := records[id]; got.KilledAt != 42 || got.AtPayload != nil {
		t.Fatalf("reasserted kill = %#v; want original time and permanent payload", got)
	}
}

func TestClearKillBaselineIsMonotonicAndRaceSafe(t *testing.T) {
	t.Parallel()
	state := openTestStore(t)
	ctx := context.Background()
	const id = "11111111-1111-4111-8111-111111111111"

	if err := state.KillUntilPrompt(ctx, id, 10, 2); err != nil {
		t.Fatal(err)
	}
	if err := state.KillUntilPrompt(ctx, id, 11, 3); err != nil {
		t.Fatal(err)
	}
	records, err := state.KilledRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := records[id]; got.KilledAt != 11 || got.AtPayload == nil || *got.AtPayload != 3 {
		t.Fatalf("repeated clear kill = %#v, want hidden_at=11 baseline=3", got)
	}
	if removed, err := state.UnkillIfPayload(ctx, id, 2); err != nil || removed {
		t.Fatalf("stale conditional unkill = %v, %v; want false, nil", removed, err)
	}

	if err := state.Kill(ctx, id, 12); err != nil {
		t.Fatal(err)
	}
	if err := state.KillUntilPrompt(ctx, id, 13, 4); err != nil {
		t.Fatal(err)
	}
	records, err = state.KilledRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := records[id]; got.KilledAt != 12 || got.AtPayload != nil {
		t.Fatalf("clear weakened permanent kill = %#v", got)
	}

	const expiring = "22222222-2222-4222-8222-222222222222"
	if err := state.KillUntilPrompt(ctx, expiring, 20, 5); err != nil {
		t.Fatal(err)
	}
	if removed, err := state.UnkillIfPayload(ctx, expiring, 5); err != nil || !removed {
		t.Fatalf("matching conditional unkill = %v, %v; want true, nil", removed, err)
	}
}

func TestDegradedStoreRejectsOperatorStateChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := paths.Values{
		StateDB: filepath.Join(blocker, "pfm.db"),
		Home:    filepath.Join(root, "home"),
	}
	ctx := context.Background()
	state := OpenSharedState(ctx, values)
	t.Cleanup(func() { _ = state.Close() })

	if state.Degraded() == nil {
		t.Fatal("Degraded() = nil, want the reason the database is unusable")
	}
	if err := state.Kill(ctx, "not-written", 7); err == nil {
		t.Fatal("degraded Kill() reported success")
	}
	if _, err := state.KilledAt(ctx); err == nil {
		t.Fatal("degraded KilledAt() reported an empty set instead of its lookup failure")
	}
}

// child-add / child-list / child-clear, in the shapes chat.sh writes them:
// a bare socket for a detached teammate (chat.sh:1362) and "<socket>\t<pane>"
// for one sharing this chat's server (chat.sh:435).
func TestChildrenRoundTripInCCDBShShapes(t *testing.T) {
	t.Parallel()
	state := openTestStore(t)
	ctx := context.Background()

	if err := state.AddChild(ctx, KindNew, "chat-1", "cc-new-worker", 10); err != nil {
		t.Fatal(err)
	}
	if err := state.AddChild(ctx, KindPane, "chat-1", "cc-1-1-1\t%3", 11); err != nil {
		t.Fatal(err)
	}
	// Repeat registration remains one row.
	if err := state.AddChild(ctx, KindNew, "chat-1", "cc-new-worker", 12); err != nil {
		t.Fatal(err)
	}

	detached, fromTable, err := state.Children(ctx, KindNew, "chat-1")
	if err != nil || !fromTable {
		t.Fatalf("Children(new) fromTable=%v err=%v", fromTable, err)
	}
	if !reflect.DeepEqual(detached, []string{"cc-new-worker"}) {
		t.Fatalf("Children(new) = %v", detached)
	}
	panes, _, err := state.Children(ctx, KindPane, "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(panes, []string{"cc-1-1-1\t%3"}) {
		t.Fatalf("Children(pane) = %v", panes)
	}

	if err := state.ClearChildren(ctx, KindNew, "chat-1"); err != nil {
		t.Fatal(err)
	}
	detached, _, err = state.Children(ctx, KindNew, "chat-1")
	if err != nil || len(detached) != 0 {
		t.Fatalf("Children(new) after clear = %v, %v", detached, err)
	}
	if panes, _, err = state.Children(ctx, KindPane, "chat-1"); err != nil ||
		len(panes) != 1 {
		t.Fatalf("clearing one kind cleared the other: %v, %v", panes, err)
	}
}

func TestBranchSeatMarkersRoundTripWithoutChangingTheSchema(t *testing.T) {
	t.Parallel()
	state := openTestStore(t)
	ctx := context.Background()
	const socket = "cc-1800000000-42-7"
	if err := state.RecordBranchSeat(ctx, socket, "parent-id", 123); err != nil {
		t.Fatal(err)
	}
	seats, err := state.BranchSeats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := seats[socket]; got.Socket != socket || got.Parent != "parent-id" || got.CreatedAt != 123 {
		t.Fatalf("branch seat = %#v", got)
	}
	if err := state.ClearBranchSeat(ctx, socket); err != nil {
		t.Fatal(err)
	}
	seats, err = state.BranchSeats(ctx)
	if err != nil || len(seats) != 0 {
		t.Fatalf("branch seats after clear = %#v err=%v", seats, err)
	}
}

// openTestStore opens the in-memory shared state (OpenSharedStateMemory), migrated as the file
// is: the store a test of the queries runs on.
func openTestStore(t *testing.T) *Store {
	t.Helper()

	state := OpenSharedStateMemory(context.Background())
	if err := state.Degraded(); err != nil {
		t.Fatalf("OpenSharedStateMemory() degraded: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state
}

func queryColumn(t *testing.T, state *Store, query string) []string {
	t.Helper()

	rows, err := state.db.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}
