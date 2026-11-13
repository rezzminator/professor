package fleetdb

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestSeatSchema(t *testing.T) {
	state := openTestStore(t)
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
	state := openTestStore(t)
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
	// A Herdr fork's lineage is keyed by its seat: the seat's end takes it, another fork's stays.
	for _, id := range []string{seat.SeatID, "other-fork"} {
		if err := state.RecordBranchSeat(ctx, id, "parent-id", 123); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.DeleteSeat(ctx, seat.SeatID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := state.Seat(ctx, seat.SeatID); err != nil || found {
		t.Fatalf("deleted lookup = %v,%v", found, err)
	}
	if forks, err := state.BranchSeats(ctx); err != nil || !reflect.DeepEqual(forks, map[string]BranchSeat{
		"other-fork": {Socket: "other-fork", Parent: "parent-id", CreatedAt: 123},
	}) {
		t.Fatalf("branch seats after the seat's delete = %#v, %v; want other-fork's alone", forks, err)
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
	state := openTestStore(t)
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
	state := openTestStore(t)
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

// A testjail home's shared state is served from memory through the marker the
// SQLite door leaves at its path, so a reader that stats the path first finds
// the seat, and a second spelling of the directory (macOS reaches its temp
// directory through /private) reaches the same database.
func TestReadSeatsFindsTheSeatTheDoorServesFromMemory(t *testing.T) {
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	state := OpenSharedState(ctx, values)
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	if err := state.PutSeat(ctx, Seat{
		SeatID: "seat-memory", SessionID: "session-memory", Engine: "claude",
		Backend: "tmux", TmuxSocket: "tmux-fixture", TmuxPane: "%1", State: "live",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadSeats(ctx, values)
	if err != nil || len(rows) != 1 || rows[0].SeatID != "seat-memory" {
		t.Fatalf("ReadSeats(%s) = %+v, %v; want the one seat seat-memory", values.StateDB, rows, err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Dir(values.StateDB), link); err != nil {
		t.Fatal(err)
	}
	linked, err := sqlitedb.OpenStore(ctx, filepath.Join(link, "pfm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = linked.Close() })
	var seatID, mode string
	if err := linked.QueryRowContext(ctx, "SELECT seat_id FROM seat").
		Scan(&seatID); err != nil ||
		seatID != "seat-memory" {
		t.Fatalf("seat through %s = %q, %v; want seat-memory", link, seatID, err)
	}
	if err := linked.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "memory" {
		t.Fatalf("journal_mode through %s = %q, %v; want memory", link, mode, err)
	}
}

func TestSeatPaneHistoricalDuplicatesRefuse(t *testing.T) {
	state := openTestStore(t)
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
	state := openTestStore(t)
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
			db := openTestStore(t)
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
			db := openTestStore(t)
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
			db := openTestStore(t)
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
