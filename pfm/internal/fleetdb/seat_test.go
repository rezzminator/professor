package fleetdb

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestSeatSchema(t *testing.T) {
	state, _ := openTestStore(t)
	columns := queryColumn(t, state, "SELECT name FROM pragma_table_info('seat') ORDER BY cid")
	want := []string{
		"seat_id",
		"session_id",
		"engine",
		"backend",
		"tmux_socket",
		"tmux_pane",
		"herdr_socket",
		"herdr_pane",
		"herdr_workspace",
		"name",
		"state",
		"reload_pending",
		"lost_note",
		"updated_at",
		"herdr_terminal_id",
		"seat_host_pid",
		"seat_host_start",
		"seat_host_birth",
		"end_pending",
	}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("seat columns = %v, want %v", columns, want)
	}
}

func TestSeatCRUD(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()
	seat := Seat{
		SeatID:         "seat-1",
		SessionID:      "6fafd8a7-5059-4288-bf58-23e60c5fdb88",
		Engine:         "cc",
		Backend:        "herdr",
		HerdrSocket:    "/tmp/x/herdr.sock",
		HerdrPane:      "w6:p3",
		HerdrWorkspace: "w6",
		Name:           "REVIEW",
		State:          "live",
		UpdatedAt:      123,
	}
	if err := state.PutSeat(ctx, seat); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []func() (Seat, bool, error){func() (Seat, bool, error) { return state.Seat(ctx, seat.SeatID) }, func() (Seat, bool, error) { return state.SeatBySession(ctx, seat.SessionID) }, func() (Seat, bool, error) { return state.SeatByPane(ctx, seat.HerdrSocket, seat.HerdrPane) }} {
		got, found, err := lookup()
		if err != nil || !found || !reflect.DeepEqual(got, seat) {
			t.Fatalf("lookup = %+v,%v,%v; want %+v", got, found, err, seat)
		}
	}
	seat.Name = "INTEGRATION"
	seat.ReloadPending = true
	if err := state.PutSeat(ctx, seat); err != nil {
		t.Fatal(err)
	}
	got, err := state.Seats(ctx)
	if err != nil || len(got) != 1 || got[0].Name != "INTEGRATION" || !got[0].ReloadPending {
		t.Fatalf("seats = %+v, %v", got, err)
	}
	for _, bad := range []Seat{{SeatID: "bad-backend", Backend: "other", State: "live"}, {SeatID: "bad-state", Backend: "herdr", State: "other"}, {Backend: "herdr", State: "live"}} {
		if err := state.PutSeat(ctx, bad); err == nil {
			t.Fatalf("PutSeat(%+v) accepted invalid seat", bad)
		}
	}
	if err := state.DeleteSeat(ctx, seat.SeatID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := state.Seat(ctx, seat.SeatID); err != nil || found {
		t.Fatalf("deleted lookup = %v,%v", found, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.PutSeat(ctx, seat); err == nil {
		t.Fatal("write on closed database succeeded")
	}
	if _, _, err := state.Seat(ctx, seat.SeatID); err == nil {
		t.Fatal("read on closed database became absence")
	}
}

func TestPutSeatRetiresTmuxRowsForMovedSessionOnlyAfterInsert(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()
	for _, seat := range []Seat{
		{SeatID: "cc-old", SessionID: "S1", Backend: "tmux", State: "live", UpdatedAt: 100},
		{SeatID: "cc-other", SessionID: "S2", Backend: "tmux", State: "live"},
		{SeatID: "herdr-owner", SessionID: "S3", Backend: "herdr", State: "live", HerdrSocket: "/h.sock", HerdrPane: "w1:p1"},
		{SeatID: "cc-refused", SessionID: "S4", Backend: "tmux", State: "live", UpdatedAt: 100},
	} {
		if err := state.PutSeat(ctx, seat); err != nil {
			t.Fatal(err)
		}
	}
	moved := Seat{
		SeatID:      "new-herdr",
		SessionID:   "S1",
		Backend:     "herdr",
		State:       "live",
		HerdrSocket: "/h.sock",
		HerdrPane:   "w1:p2",
		UpdatedAt:   200,
	}
	if err := state.PutSeat(ctx, moved); err != nil {
		t.Fatal(err)
	}
	if _, found, err := state.Seat(ctx, "cc-old"); err != nil || found {
		t.Fatalf("old tmux row found=%v err=%v", found, err)
	}
	for _, id := range []string{"cc-other", "herdr-owner", "new-herdr"} {
		if _, found, err := state.Seat(ctx, id); err != nil || !found {
			t.Fatalf("seat %s found=%v err=%v", id, found, err)
		}
	}

	if err := state.PutSeat(
		ctx,
		Seat{
			SeatID:      "refused-herdr",
			SessionID:   "S4",
			Backend:     "herdr",
			State:       "live",
			HerdrSocket: "/h.sock",
			HerdrPane:   "w1:p1",
		},
	); err == nil {
		t.Fatal("duplicate live Herdr pane was accepted")
	}
	if _, found, err := state.Seat(ctx, "cc-refused"); err != nil || !found {
		t.Fatalf("refused insert retired prior row: found=%v err=%v", found, err)
	}
	if err := state.PutSeat(ctx, Seat{
		SeatID: "cc-replacement", SessionID: "S4", Backend: "tmux", State: "live", UpdatedAt: 101,
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := state.Seat(ctx, "cc-refused"); err != nil || found {
		t.Fatalf("replacement left prior tmux row: found=%v err=%v", found, err)
	}
}

func TestSeatByPaneIgnoresFailedOwner(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()
	failed := Seat{
		SeatID:      "failed",
		Backend:     "herdr",
		State:       "failed",
		HerdrSocket: "/test.sock",
		HerdrPane:   "w1:p1",
		UpdatedAt:   100,
	}
	live := failed
	live.SeatID, live.State, live.UpdatedAt = "live", "live", 0
	for _, seat := range []Seat{failed, live} {
		if err := state.PutSeat(ctx, seat); err != nil {
			t.Fatal(err)
		}
	}
	got, found, err := state.SeatByPane(ctx, live.HerdrSocket, live.HerdrPane)
	if err != nil || !found || got.SeatID != live.SeatID {
		t.Fatalf("failed owner shadowed new seat: %+v %v %v", got, found, err)
	}
}

func TestSeatConcurrentUpdates(t *testing.T) {
	state, _ := openTestStore(t)
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
	state, values := openTestStore(t)
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
	state, values := openTestStore(t)
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
			state, values := openTestStore(t)
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

func TestSeatPaneHistoricalDuplicatesRefuse(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()
	owner := Seat{
		SeatID:      "first",
		Backend:     "herdr",
		State:       "live",
		HerdrSocket: "/fixture/herdr.sock",
		HerdrPane:   "w6:p3",
	}
	if err := state.PutSeat(ctx, owner); err != nil {
		t.Fatal(err)
	}
	// Model an already-duplicated version-6 database, bypassing the mutation API.
	if _, err := state.db.ExecContext(
		ctx,
		"INSERT INTO seat ("+seatColumns+") SELECT 'second',"+strings.TrimPrefix(
			seatColumns,
			"seat_id,",
		)+" FROM seat WHERE seat_id='first'",
	); err != nil {
		t.Fatal(err)
	}
	got, found, err := state.SeatByPane(ctx, owner.HerdrSocket, owner.HerdrPane)
	if err == nil || !strings.Contains(err.Error(), "multiple live seats") || found || got.SeatID != "" {
		t.Fatalf("ambiguous owner=%+v found=%v err=%v", got, found, err)
	}
}

func TestSeatUpdateRejectsStaleCanonicalAddress(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()
	before := Seat{SeatID: "moving", Backend: "herdr", State: "live", HerdrPane: "w1:p1", HerdrSocket: "fixture"}
	if err := state.PutSeat(ctx, before); err != nil {
		t.Fatal(err)
	}
	moved := before
	moved.HerdrPane = "w2:p4"
	if err := state.UpdateSeat(ctx, before, moved); err != nil {
		t.Fatal(err)
	}
	stale := before
	stale.HerdrPane = "w3:p9"
	if err := state.UpdateSeat(ctx, before, stale); err == nil {
		t.Fatal("stale canonical writer overwrote moved seat")
	}
	got, _, err := state.Seat(ctx, before.SeatID)
	if err != nil || got.HerdrPane != moved.HerdrPane {
		t.Fatalf("canonical seat=%+v err=%v", got, err)
	}
}

// The reader must work before any writer has added ownership columns.
func TestReadSeatsPreOwnershipV6(t *testing.T) {
	for _, added := range []string{"", "ALTER TABLE seat ADD COLUMN herdr_terminal_id TEXT NOT NULL DEFAULT '';"} {
		t.Run(fmt.Sprintf("partial=%t", added != ""), func(t *testing.T) {
			values := paths.Values{StateDB: filepath.Join(t.TempDir(), "legacy.db")}
			seedSharedState(t, values.StateDB, migrationV6+added+`
PRAGMA user_version=6;
INSERT INTO seat(seat_id,session_id,engine,backend,tmux_socket,tmux_pane,herdr_socket,herdr_pane,herdr_workspace,name,state,reload_pending,lost_note,updated_at)
VALUES('legacy','session','claude','herdr','','','fixture.sock','w1:p2','w1','OLD','live',0,'',42);`)
			before, err := os.ReadFile(values.StateDB)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := ReadSeats(context.Background(), values)
			want := []Seat{
				{
					SeatID:         "legacy",
					SessionID:      "session",
					Engine:         "claude",
					Backend:        "herdr",
					HerdrSocket:    "fixture.sock",
					HerdrPane:      "w1:p2",
					HerdrWorkspace: "w1",
					Name:           "OLD",
					State:          "live",
					UpdatedAt:      42,
				},
			}
			if err != nil || !reflect.DeepEqual(rows, want) {
				t.Fatalf("pre-ownership read=%+v err=%v, want %+v", rows, err, want)
			}
			store := OpenSharedStateReadOnly(context.Background(), values)
			defer func() { _ = store.Close() }()
			listed, listErr := store.Seats(context.Background())
			if listErr != nil || !reflect.DeepEqual(listed, want) {
				t.Errorf("read-only Store seats=%+v err=%v, want %+v", listed, listErr, want)
			}
			for _, lookup := range []func(context.Context) (Seat, bool, error){
				func(ctx context.Context) (Seat, bool, error) { return store.Seat(ctx, "legacy") },
				func(ctx context.Context) (Seat, bool, error) { return store.SeatBySession(ctx, "session") },
				func(ctx context.Context) (Seat, bool, error) { return store.SeatByPane(ctx, "fixture.sock", "w1:p2") },
			} {
				got, found, lookupErr := lookup(context.Background())
				if lookupErr != nil || !found || got != want[0] {
					t.Errorf("read-only Store lookup=%+v found=%v err=%v, want %+v", got, found, lookupErr, want[0])
				}
			}
			after, err := os.ReadFile(values.StateDB)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("read-only enumeration wrote the old-schema database")
			}
		})
	}
}

func TestSeatFailurePublicationRejectsChangedBinding(t *testing.T) {
	for _, change := range []string{"host", "terminal", "end"} {
		t.Run(change, func(t *testing.T) {
			db, _ := openTestStore(t)
			ctx := context.Background()
			before := Seat{
				SeatID:          "failure",
				SessionID:       "conversation",
				Backend:         "herdr",
				State:           "live",
				HerdrSocket:     "fixture",
				HerdrPane:       "w1:p1",
				HerdrTerminalID: "terminal",
				SeatHostPID:     10,
				SeatHostStart:   20,
				SeatHostBirth:   30,
			}
			if err := db.PutSeat(ctx, before); err != nil {
				t.Fatal(err)
			}
			next := before
			switch change {
			case "host":
				next.SeatHostPID++
			case "terminal":
				next.HerdrTerminalID = "replacement"
			case "end":
				next.EndPending = true
			}
			if err := db.UpdateSeat(ctx, before, next); err != nil {
				t.Fatal(err)
			}
			failed := before
			failed.State = "failed"
			failed.LostNote = "old attempt"
			if err := db.UpdateSeat(ctx, before, failed); err == nil {
				t.Fatal("stale failure publication overwrote changed binding")
			}
			got, found, err := db.Seat(ctx, before.SeatID)
			if err != nil || !found || got != next {
				t.Fatalf("binding=%+v found=%v err=%v want=%+v", got, found, err, next)
			}
		})
	}
}

func TestSeatLifecycleIgnoresHeartbeatConflict(t *testing.T) {
	for _, lifecycle := range []string{"start", "failure", "heartbeat"} {
		t.Run(lifecycle, func(t *testing.T) {
			db, _ := openTestStore(t)
			ctx := context.Background()
			before := Seat{SeatID: "heartbeat", Backend: "herdr", State: "live", SessionID: "session", UpdatedAt: 1}
			if err := db.PutSeat(ctx, before); err != nil {
				t.Fatal(err)
			}
			concurrent := before
			concurrent.UpdatedAt = 4
			concurrent.Name = "renamed"
			if err := db.UpdateSeat(ctx, before, concurrent); err != nil {
				t.Fatal(err)
			}
			after := before
			after.UpdatedAt = 3
			switch lifecycle {
			case "start":
				after.LostNote = "started"
			case "failure":
				after.State = "failed"
				after.LostNote = "exec failed"
			}
			if err := db.UpdateSeat(ctx, before, after); err != nil {
				t.Fatalf("metadata conflict rejected lifecycle: %v", err)
			}
			got, found, err := db.Seat(ctx, before.SeatID)
			if err != nil || !found || got.State != after.State || got.LostNote != after.LostNote ||
				got.Name != "renamed" ||
				got.UpdatedAt != 4 {
				t.Fatalf("composed lifecycle=%+v found=%v err=%v", got, found, err)
			}
		})
	}
}

func TestSeatLifecycleComposesLearnedSession(t *testing.T) {
	for _, phase := range []string{"retry-start", "reload-start", "failure"} {
		t.Run(phase, func(t *testing.T) {
			db, _ := openTestStore(t)
			ctx := context.Background()
			before := Seat{
				SeatID:          "learning",
				Backend:         "herdr",
				State:           "failed",
				LostNote:        "retry",
				SessionID:       "provisional",
				Engine:          "cc",
				HerdrSocket:     "fixture",
				HerdrPane:       "w1:p1",
				HerdrTerminalID: "terminal",
				SeatHostPID:     10,
				SeatHostStart:   20,
			}
			before.ReloadPending = phase == "reload-start"
			if err := db.PutSeat(ctx, before); err != nil {
				t.Fatal(err)
			}
			learned := before
			learned.SessionID = "engine-session"
			if err := db.UpdateSeat(ctx, before, learned); err != nil {
				t.Fatal(err)
			}
			after := before
			after.State, after.LostNote, after.ReloadPending = "live", "", false
			if phase == "failure" {
				after.State, after.LostNote = "failed", "exec failed"
			}
			if err := db.UpdateSeat(ctx, before, after); err != nil {
				t.Fatalf("SessionID learning rejected %s callback: %v", phase, err)
			}
			want := after
			want.SessionID = learned.SessionID
			got, found, err := db.Seat(ctx, before.SeatID)
			if err != nil || !found || got != want {
				t.Fatalf("started authority=%+v want=%+v found=%t err=%v", got, want, found, err)
			}
		})
	}
}
