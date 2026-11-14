package gather

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A seat whose Herdr socket file is gone (Herdr restarted under it) retires
// once its supervisor, never stamped, runs nowhere (a stamped one is
// TestProvedListingRetiresDeadSeatOnly's stopped-dead); a socket that
// refuses keeps a dead stamped supervisor's own note; a socket that refuses
// an unstamped seat, a supervisor that runs, an unreadable process table, a
// read-only listing and an end in flight leave the durable row as it is.
func TestProvedListingRetiresSeatOfVanishedSocket(t *testing.T) {
	absent := &fakeProcFS{processes: map[int]fakeProcess{
		10: {cmdline: []string{"zsh"}, stat: ProcStat{ParentPID: 1, StartTime: 3}},
	}}
	supervised := &fakeProcFS{processes: map[int]fakeProcess{
		50: {cmdline: []string{"pfm", "internal", "seat-host", "legacy"}, stat: ProcStat{ParentPID: 1, StartTime: 4}},
	}}
	for _, tc := range []struct {
		name     string
		refused  bool
		stamped  bool
		unbound  bool // a reservation past its birth window that never named a pane
		birthing bool // a reservation still inside its birth window, no pane named yet
		readOnly bool
		pending  bool
		proc     ProcFS
		wantNote string // "" leaves the stored row unchanged
		wantDead bool
	}{
		{name: "never stamped, no supervisor anywhere", proc: absent, wantNote: "gone", wantDead: true},
		{
			name: "stamped supervisor dead, socket refuses", refused: true, stamped: true, proc: absent,
			wantNote: retireLostNote, wantDead: true,
		},
		{name: "never stamped, never named a pane", unbound: true, proc: absent, wantNote: failedBirthNote},
		{name: "never stamped, socket refuses", refused: true, proc: absent},
		{name: "never stamped, birth window open", birthing: true, proc: absent},
		{name: "never stamped, supervisor runs", proc: supervised},
		{name: "process table unreadable", proc: pidsUnreadableProc{absent}},
		{name: "read-only listing", readOnly: true, proc: absent, wantDead: true},
		{name: "end in flight", pending: true, proc: absent, wantDead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			socket := filepath.Join(testjail.ShortRoot(t), "herdr-api.sock")
			if tc.refused {
				listener, err := net.Listen("unix", socket)
				if err != nil {
					t.Fatal(err)
				}
				listener.(*net.UnixListener).SetUnlinkOnClose(false)
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
			}
			seat := fleetdb.Seat{
				SeatID: "legacy", SessionID: "legacy", Engine: "cc", Backend: "herdr", State: "live",
				HerdrSocket: socket, HerdrPane: "w1:p9", HerdrWorkspace: "w1", EndPending: tc.pending,
			}
			if tc.unbound {
				seat.HerdrPane, seat.HerdrWorkspace = "", ""
			}
			if tc.birthing {
				seat.HerdrPane, seat.HerdrWorkspace, seat.UpdatedAt = "", "", time.Now().Unix()
			}
			if tc.stamped {
				seat.SeatHostPID, seat.SeatHostStart = 30, 9
			}
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			rows := proveHerdrSeats(ctx, db, tc.proc, probeHerdrSeatRows(ctx, []fleetdb.Seat{seat}), tc.readOnly)
			gone := "Herdr socket " + socket + " is gone and the seat's supervisor with it"
			wantNote := tc.wantNote
			if wantNote == "gone" {
				wantNote = "retired by pfm scan: " + gone + "; resume recipe retained"
			}
			if tc.unbound {
				stored, found, err := db.Seat(ctx, seat.SeatID)
				if len(rows) != 0 || err != nil || !found || stored.State != "failed" || stored.LostNote != wantNote {
					t.Fatalf("rows=%+v stored %+v found=%t err=%v, want no row and failed with %q",
						rows, stored, found, err, wantNote)
				}
				return
			}
			if len(rows) != 1 || rows[0].Err == nil {
				t.Fatalf("rows=%+v, want one row with its error", rows)
			}
			gotErr := rows[0].Err.Error()
			switch {
			case tc.wantDead && !tc.refused:
				if want := "HERDR DEAD legacy: " + gone; gotErr != want || rows[0].Seat.State != "lost" {
					t.Fatalf("row state %q err %q, want lost and %q", rows[0].Seat.State, gotErr, want)
				}
			case tc.wantDead:
				if want := "HERDR DEAD legacy: saved supervisor exited"; gotErr != want {
					t.Fatalf("row err %q, want %q", gotErr, want)
				}
			default:
				if want := "HERDR UNREACHABLE " + socket + ": "; !strings.HasPrefix(gotErr, want) ||
					rows[0].Seat.State != "live" {
					t.Fatalf("row state %q err %q, want live and the prefix %q", rows[0].Seat.State, gotErr, want)
				}
			}
			stored, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil || !found {
				t.Fatalf("stored seat found=%t err=%v", found, err)
			}
			if tc.birthing {
				// The kill door's judgement of the same seat: a birth in flight is never dead.
				if retired, err := HerdrSeatRetired(ctx, tc.proc, seat); retired || err == nil {
					t.Fatalf("HerdrSeatRetired = %t, %v; want false with the dial error", retired, err)
				}
			}
			if wantNote == "" {
				if stored != seat {
					t.Fatalf("stored %+v, want unchanged %+v", stored, seat)
				}
				return
			}
			if stored.State != "lost" || stored.LostNote != wantNote {
				t.Fatalf("stored state %q note %q, want lost and %q", stored.State, stored.LostNote, wantNote)
			}
		})
	}
}
