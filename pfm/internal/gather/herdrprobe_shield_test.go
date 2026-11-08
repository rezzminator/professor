package gather

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A pane herdr-shield holds shows Herdr only the attach client: its owner is
// proved through the holder bound to that pane, whose shell runs the
// supervisor, and never through a holder bound to another pane. A seat whose
// pane is gone follows its holder to the pane `herdr-shield attach` moved it to,
// never a holder whose child is a reused pid, and never past a failed listing.
func TestProveHerdrSeatThroughHerdrShield(t *testing.T) {
	for _, tc := range []struct {
		name, bound, seatPane, seatTerminal string
		// childStart is the start time herdr-shield records for its child, pid 10
		// (started 3); shieldExit nonzero makes `herdr-shield list --json` fail.
		childStart, shieldExit int
		// diagnostic is the whole proof error, {socket} the Herdr socket; empty
		// when the proof owns the seat.
		diagnostic string
	}{
		{
			name: "holder bound to the pane", bound: "w1:p1", seatPane: "w1:p1", seatTerminal: "terminal-one",
			childStart: 3,
		},
		{
			name: "holder bound to another pane", bound: "w1:p9", seatPane: "w1:p1", seatTerminal: "terminal-one",
			childStart: 3, diagnostic: "seat owner supervisor is outside pane process tree",
		},
		{
			name: "holder moved from a closed pane", bound: "w1:p1", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 3,
		},
		{
			name: "holder moved to a pane Herdr does not list", bound: "w1:p9", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 3, diagnostic: "seat owner ownership unproved: terminal not present in pane.list",
		},
		{
			name: "moved holder whose child is a reused pid", bound: "w1:p1", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 4, diagnostic: "seat owner ownership unproved: terminal not present in pane.list",
		},
		{
			name: "holders unlisted for a closed pane", bound: "w1:p1", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 3, shieldExit: 1,
			diagnostic: "seat owner ownership unproved: terminal not present in pane.list; follow a moved herdr-shield holder: " +
				"herdr-shield list --json against {socket}: exit 1: user manager unavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			socket, _ := herdrOwnershipSocketInfo(
				t,
				`{"result":{"type":"pane_list","panes":[{"pane_id":"w1:p1","workspace_id":"w1",`+
					`"terminal_id":"terminal-one","tab_id":"w1:t1","focused":true}]}}`,
				map[string]any{
					"pane_id": "w1:p1", "shell_pid": 7,
					"foreground_processes": []any{map[string]any{"pid": 8, "name": "herdr-shield"}},
				},
			)
			listing := testjail.HerdrShieldListing(5, 2, 10, tc.childStart, tc.bound, socket)
			if tc.shieldExit != 0 {
				listing = "user manager unavailable"
			}
			testjail.StageHerdrShield(t, listing, tc.shieldExit)
			seat := fleetdb.Seat{
				SeatID: "owner", Backend: "herdr", State: "live", HerdrSocket: socket,
				HerdrPane: tc.seatPane, HerdrWorkspace: "w1", HerdrTerminalID: tc.seatTerminal,
				SeatHostPID: 20, SeatHostStart: 9,
			}
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			// Herdr's pane shell 7 runs only the attach client 8; holder 5's child
			// shell 10 runs the supervisor 20 and, in its foreground, the engine 41.
			proc := &fakeProcFS{processes: map[int]fakeProcess{
				5:  {stat: ProcStat{ParentPID: 1, StartTime: 2}},
				7:  {stat: ProcStat{ParentPID: 1, StartTime: 4}},
				8:  {cmdline: []string{"herdr-shield"}, stat: ProcStat{ParentPID: 7, StartTime: 5}},
				10: {stat: ProcStat{ParentPID: 5, StartTime: 3, Group: 10, TerminalGroup: 41}},
				20: {
					cmdline: []string{"/jail/pfm", "internal", "seat-host", "owner"},
					stat:    ProcStat{ParentPID: 10, StartTime: 9, Group: 20, TerminalGroup: 41},
				},
				41: {
					cmdline: []string{"claude"},
					stat:    ProcStat{ParentPID: 20, StartTime: 11, Group: 41, TerminalGroup: 41},
				},
			}}
			got, owned, err := ProveHerdrSeat(ctx, db, proc, seat)
			if tc.diagnostic != "" {
				want := strings.ReplaceAll(tc.diagnostic, "{socket}", socket)
				if owned || err == nil || err.Error() != want {
					t.Fatalf("proof owned=%v err=%v; want error %q", owned, err, want)
				}
				return
			}
			if !owned || err != nil {
				t.Fatalf("proof owned=%v err=%v; want owned", owned, err)
			}
			stored, found, err := db.Seat(ctx, "owner")
			if err != nil || !found {
				t.Fatalf("read proved seat: found=%v err=%v", found, err)
			}
			for _, row := range []fleetdb.Seat{got, stored} {
				if row.HerdrPane != "w1:p1" || row.HerdrTerminalID != "terminal-one" || row.SeatHostPID != 20 {
					t.Fatalf("proved seat=%+v, want pane w1:p1 terminal-one supervisor 20", row)
				}
			}
		})
	}
}
