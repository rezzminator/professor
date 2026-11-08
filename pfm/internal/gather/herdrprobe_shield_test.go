package gather

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A pane herdr-shield holds shows Herdr only the attach client: its owner is
// proved through the holder bound to that pane, whose shell runs the
// supervisor, and never through a holder bound to another pane. A seat whose
// pane is gone follows its holder to the pane `herdr-shield attach` moved it to,
// never a holder whose child is a reused pid, and never past a failed listing.
// Only a live supervisor can have moved: a seat whose supervisor exited, whose
// pid another process reuses, or whose birth was never saved asks herdr-shield
// nothing.
func TestProveHerdrSeatThroughHerdrShield(t *testing.T) {
	for _, tc := range []struct {
		name, bound, seatPane, seatTerminal string
		// childStart is the start time herdr-shield records for its child, pid 10
		// (started 3); shieldExit nonzero makes `herdr-shield list --json` fail.
		childStart, shieldExit int
		// supervisorStart is the start time pid 20 shows, 0 when it exited;
		// savedStart is the seat's saved supervisor start, 0 for none.
		supervisorStart, savedStart uint64
		// diagnostic is the whole proof error, {socket} the Herdr socket; empty
		// when the proof owns the seat, or with gone when it disowns it cleanly.
		diagnostic string
		gone       bool
		// shieldRuns is how many times the proof ran herdr-shield.
		shieldRuns int
	}{
		{
			name: "holder bound to the pane", bound: "w1:p1", seatPane: "w1:p1", seatTerminal: "terminal-one",
			childStart: 3, supervisorStart: 9, savedStart: 9, shieldRuns: 1,
		},
		{
			name: "holder bound to another pane", bound: "w1:p9", seatPane: "w1:p1", seatTerminal: "terminal-one",
			childStart: 3, supervisorStart: 9, savedStart: 9, shieldRuns: 1,
			diagnostic: "seat owner supervisor is outside pane process tree",
		},
		{
			name: "holder moved from a closed pane", bound: "w1:p1", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 3, supervisorStart: 9, savedStart: 9, shieldRuns: 1,
		},
		{
			name: "holder moved to a pane Herdr does not list", bound: "w1:p9", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 3, supervisorStart: 9, savedStart: 9, shieldRuns: 1,
			diagnostic: "seat owner ownership unproved: terminal not present in pane.list",
		},
		{
			name: "moved holder whose child is a reused pid", bound: "w1:p1", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 4, supervisorStart: 9, savedStart: 9, shieldRuns: 1,
			diagnostic: "seat owner ownership unproved: terminal not present in pane.list",
		},
		{
			name: "holders unlisted for a closed pane", bound: "w1:p1", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 3, supervisorStart: 9, savedStart: 9, shieldExit: 1, shieldRuns: 1,
			diagnostic: "seat owner ownership unproved: terminal not present in pane.list; follow a moved herdr-shield holder: " +
				"herdr-shield list --json against {socket}: exit 1: user manager unavailable",
		},
		{
			name: "closed pane of a supervisor that exited", bound: "w1:p1", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 3, savedStart: 9, gone: true,
		},
		{
			name: "closed pane of a supervisor pid reused", bound: "w1:p1", seatPane: "w1:p0", seatTerminal: "terminal-old",
			childStart: 3, supervisorStart: 8, savedStart: 9, gone: true,
		},
		{
			name: "closed pane of a supervisor with no saved birth", bound: "w1:p1", seatPane: "w1:p0",
			seatTerminal: "terminal-old", childStart: 3, supervisorStart: 9,
			diagnostic: "seat owner ownership unproved: terminal not present in pane.list",
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
			runs := testjail.StageHerdrShield(t, listing, tc.shieldExit)
			seat := fleetdb.Seat{
				SeatID: "owner", Backend: "herdr", State: "live", HerdrSocket: socket,
				HerdrPane: tc.seatPane, HerdrWorkspace: "w1", HerdrTerminalID: tc.seatTerminal,
				SeatHostPID: 20, SeatHostStart: tc.savedStart,
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
					stat:    ProcStat{ParentPID: 10, StartTime: tc.supervisorStart, Group: 20, TerminalGroup: 41},
				},
				41: {
					cmdline: []string{"claude"},
					stat:    ProcStat{ParentPID: 20, StartTime: 11, Group: 41, TerminalGroup: 41},
				},
			}}
			if tc.supervisorStart == 0 {
				delete(proc.processes, 20)
			}
			got, owned, err := ProveHerdrSeat(ctx, db, proc, seat)
			if ran := runs(); ran != tc.shieldRuns {
				t.Errorf(
					"herdr-shield ran %d times for the proof (owned=%v err=%v); want %d",
					ran,
					owned,
					err,
					tc.shieldRuns,
				)
			}
			if tc.gone {
				if owned || err != nil {
					t.Fatalf("proof owned=%v err=%v; want the seat disowned without error", owned, err)
				}
				return
			}
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

// One proved listing asks herdr-shield once per Herdr socket, however many of
// its seats a `herdr-shield attach` moved or a holder holds: each seat's
// moved-pane follow and held-shell read share the listing's one run.
func TestProvedListingAsksHerdrShieldOncePerSocket(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pfm.db")
	db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: dbPath})
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
	runs := testjail.StageHerdrShield(t, testjail.HerdrShieldListing(5, 2, 10, 3, "w1:p1", socket), 0)
	// Holder 5's child shell 10 runs every seat's supervisor: one seat on the
	// held pane w1:p1, and four whose closed panes w1:p2 to w1:p5 it left behind.
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		5:  {stat: ProcStat{ParentPID: 1, StartTime: 2}},
		7:  {stat: ProcStat{ParentPID: 1, StartTime: 4}},
		8:  {cmdline: []string{"herdr-shield"}, stat: ProcStat{ParentPID: 7, StartTime: 5}},
		10: {stat: ProcStat{ParentPID: 5, StartTime: 3, Group: 10, TerminalGroup: 41}},
		41: {cmdline: []string{"claude"}, stat: ProcStat{ParentPID: 10, StartTime: 11, Group: 41, TerminalGroup: 41}},
	}}
	var seats []fleetdb.Seat
	for i := range 5 {
		id := fmt.Sprintf("owner-%d", i)
		seat := fleetdb.Seat{
			SeatID: id, Backend: "herdr", State: "live", HerdrSocket: socket,
			HerdrPane: "w1:p1", HerdrWorkspace: "w1", HerdrTerminalID: "terminal-one",
			SeatHostPID: 20 + i, SeatHostStart: 9,
		}
		if i > 0 {
			seat.HerdrPane, seat.HerdrTerminalID = fmt.Sprintf("w1:p%d", i+1), fmt.Sprintf("terminal-old-%d", i)
		}
		proc.processes[20+i] = fakeProcess{
			cmdline: []string{"/jail/pfm", "internal", "seat-host", id},
			stat:    ProcStat{ParentPID: 10, StartTime: 9, Group: 20 + i, TerminalGroup: 41},
		}
		if err := db.PutSeat(ctx, seat); err != nil {
			t.Fatal(err)
		}
		seats = append(seats, seat)
	}
	proofDB := fleetdb.OpenSharedStateReadOnly(ctx, paths.Values{StateDB: dbPath})
	defer func() {
		if err := proofDB.Close(); err != nil {
			t.Error(err)
		}
	}()
	// Every herdr-shield run takes longer than a listing stays fresh, so only the
	// listing's own hold keeps one run serving all five seats.
	shield := HerdrShield(proc)
	fake := clock.NewFake(time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC))
	shield.Clock = fake
	shield.Runner = agingRunner{Runner: shield.Runner, clock: fake, run: 3 * time.Second}
	rows := proveHerdrSeats(ctx, proofDB, proc, shield, probeHerdrSeatRows(ctx, seats), true)
	if len(rows) != len(seats) {
		t.Fatalf("listing rows=%d; want %d: %+v", len(rows), len(seats), rows)
	}
	for i, row := range rows {
		if row.Err != nil || row.Seat.SeatID != seats[i].SeatID || row.Seat.HerdrPane != "w1:p1" ||
			row.Seat.HerdrTerminalID != "terminal-one" {
			t.Fatalf(
				"row %d=%+v err=%v; want seat %s owned on w1:p1 terminal-one",
				i, row.Seat, row.Err, seats[i].SeatID,
			)
		}
	}
	if got := runs(); got != 1 {
		t.Fatalf("herdr-shield list ran %d times for one listing of %d seats on one socket; want 1", got, len(seats))
	}
}

// agingRunner moves clock on by run each time a command runs: a slow herdr-shield.
type agingRunner struct {
	deps.Runner
	clock *clock.Fake
	run   time.Duration
}

func (runner agingRunner) Run(ctx context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error) {
	runner.clock.Advance(runner.run)
	return runner.Runner.Run(ctx, argv, opts)
}
