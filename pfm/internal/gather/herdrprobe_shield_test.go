package gather

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A pane herdr-shield holds shows Herdr only the attach client: its owner is
// proved through the holder bound to that pane, whose shell runs the
// supervisor, and never through a holder bound to another pane.
func TestProveHerdrSeatThroughHerdrShield(t *testing.T) {
	for _, tc := range []struct {
		name, bound string
		owned       bool
	}{
		{"holder bound to the pane", "w1:p1", true},
		{"holder bound to another pane", "w1:p9", false},
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
			testjail.StageHerdrShield(t, testjail.HerdrShieldListing(5, 2, 10, 3, tc.bound, socket), 0)
			seat := fleetdb.Seat{
				SeatID: "owner", Backend: "herdr", State: "live", HerdrSocket: socket,
				HerdrPane: "w1:p1", HerdrWorkspace: "w1", HerdrTerminalID: "terminal-one",
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
			if owned != tc.owned || (err == nil) != tc.owned {
				t.Fatalf("proof owned=%v err=%v; want owned=%v", owned, err, tc.owned)
			}
			if owned && (got.HerdrPane != "w1:p1" || got.SeatHostPID != 20) {
				t.Fatalf("proved seat=%+v, want pane w1:p1 supervisor 20", got)
			}
		})
	}
}
