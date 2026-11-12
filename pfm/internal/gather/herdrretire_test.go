package gather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// herdrRefusalSocket answers pane.list with list and pane.get and
// pane.process_info with get, and counts the pane.get requests it answered.
func herdrRefusalSocket(t *testing.T, list, get string) (string, *atomic.Int32) {
	t.Helper()
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var gets atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				reply := `{"error":{"code":"unexpected_method","message":"` + req.Method + `"}}`
				switch req.Method {
				case "pane.list":
					reply = list
				case "pane.get":
					gets.Add(1)
					reply = get
				case "pane.process_info":
					reply = get
				}
				var body map[string]any
				_ = json.Unmarshal([]byte(reply), &body)
				body["id"] = req.ID
				_ = json.NewEncoder(conn).Encode(body)
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	return socket, &gets
}

const (
	retireListing   = `{"result":{"type":"pane_list","panes":[{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"other"}]}}`
	retirePaneGone  = `{"error":{"code":"pane_not_found","message":"pane not found"}}`
	retireLostNote  = "retired by pfm scan: saved supervisor exited; resume recipe retained"
	retireDeadLabel = "HERDR DEAD"
)

// A writable scan that proves a seat dead retires it once; the next scan
// skips it and sends Herdr no pane.get for it. Both proofs retire: a seat
// with no saved supervisor (pane absent, refused, no supervisor anywhere) and
// one whose saved supervisor's pid and kernel birth are gone.
func TestProvedListingRetiresDeadSeatOnce(t *testing.T) {
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	socket, gets := herdrRefusalSocket(t, retireListing, retirePaneGone)
	for _, seat := range []fleetdb.Seat{
		{
			SeatID: "legacy", SessionID: "legacy", Engine: "cc", Backend: "herdr", State: "live",
			HerdrSocket: socket, HerdrPane: "w1:p9", HerdrWorkspace: "w1",
		},
		{
			SeatID: "hosted", SessionID: "hosted", Engine: "cc", Backend: "herdr", State: "live",
			HerdrSocket: socket, HerdrPane: "w1:p8", HerdrWorkspace: "w1", HerdrTerminalID: "gone",
			SeatHostPID: 30, SeatHostStart: 9,
		},
	} {
		if err := db.PutSeat(ctx, seat); err != nil {
			t.Fatal(err)
		}
	}
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		10: {cmdline: []string{"zsh"}, stat: ProcStat{ParentPID: 1, StartTime: 3}},
	}}
	for scan := 1; scan <= 2; scan++ {
		seats, err := db.Seats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rows := proveHerdrSeats(ctx, db, proc, probeHerdrSeatRows(ctx, seats), false)
		if scan == 2 {
			if len(rows) != 0 {
				t.Fatalf("second scan probed retired seats again: %+v", rows)
			}
			break
		}
		if len(rows) != 2 {
			t.Fatalf("first scan rows=%+v", rows)
		}
		for _, row := range rows {
			if row.Seat.State != "lost" || row.Err == nil || !strings.Contains(row.Err.Error(), retireDeadLabel) ||
				strings.Contains(row.Err.Error(), "retire dead seat") {
				t.Fatalf("dead seat row: %+v err=%v", row.Seat, row.Err)
			}
		}
	}
	if got := gets.Load(); got != 1 {
		t.Fatalf("pane.get sent %d times over two scans, want 1", got)
	}
	for _, id := range []string{"legacy", "hosted"} {
		stored, found, err := db.Seat(ctx, id)
		if err != nil || !found || stored.State != "lost" || stored.LostNote != retireLostNote {
			t.Fatalf("seat %s not retired: %+v found=%t err=%v", id, stored, found, err)
		}
	}
}

// pidsUnreadableProc cannot enumerate the process table: supervisor absence
// is unknown, never proven.
type pidsUnreadableProc struct{ *fakeProcFS }

func (pidsUnreadableProc) PIDs() ([]int, error) { return nil, fs.ErrPermission }

// Death not positively proved, or proved for a seat an end or reload owns,
// never retires the durable row.
func TestProvedListingNeverRetiresUnprovedDeath(t *testing.T) {
	absent := &fakeProcFS{processes: map[int]fakeProcess{
		10: {cmdline: []string{"zsh"}, stat: ProcStat{ParentPID: 1, StartTime: 3}},
	}}
	for _, tc := range []struct {
		name    string
		get     string
		proc    ProcFS
		pending func(*fleetdb.Seat)
		dead    bool
	}{
		{name: "other-refusal", get: `{"error":{"code":"internal","message":"boom"}}`, proc: absent},
		{
			name: "pane-answers",
			get:  `{"result":{"type":"pane_info","pane":{"pane_id":"w1:p9","workspace_id":"w1","terminal_id":"t"}}}`,
			proc: absent,
		},
		{name: "supervisor-present", get: retirePaneGone, proc: &fakeProcFS{processes: map[int]fakeProcess{
			50: {cmdline: []string{"pfm", "internal", "seat-host", "legacy"}, stat: ProcStat{ParentPID: 1, StartTime: 4}},
		}}},
		{name: "proc-unreadable", get: retirePaneGone, proc: pidsUnreadableProc{absent}},
		{name: "end-pending", get: retirePaneGone, proc: absent, dead: true, pending: func(seat *fleetdb.Seat) {
			seat.EndPending = true
		}},
		{name: "reload-pending", get: retirePaneGone, proc: absent, dead: true, pending: func(seat *fleetdb.Seat) {
			seat.ReloadPending = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			socket, _ := herdrRefusalSocket(t, retireListing, tc.get)
			seat := fleetdb.Seat{
				SeatID: "legacy", SessionID: "legacy", Engine: "cc", Backend: "herdr", State: "live",
				HerdrSocket: socket, HerdrPane: "w1:p9", HerdrWorkspace: "w1",
			}
			if tc.pending != nil {
				tc.pending(&seat)
			}
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			rows := proveHerdrSeats(
				ctx,
				db,
				tc.proc,
				probeHerdrSeatRows(ctx, []fleetdb.Seat{seat}),
				false,
			)
			if len(rows) != 1 || rows[0].Err == nil ||
				strings.Contains(rows[0].Err.Error(), retireDeadLabel) != tc.dead {
				t.Fatalf("row=%+v (want dead=%t)", rows, tc.dead)
			}
			stored, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil || !found || stored != seat {
				t.Fatalf("unproved death retired the seat: %+v want %+v found=%t err=%v", stored, seat, found, err)
			}
		})
	}
}

// An unpublished reservation past its birth window, with no pane and no
// supervisor, is dead and retires; a row no longer live never does.
func TestRetireDeadHerdrSeatWindowAndState(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		state   string
		updated int64
		retired bool
	}{
		{
			name: "stale-reservation", state: "live",
			updated: time.Now().Add(-herdrBirthGrace - time.Minute).Unix(), retired: true,
		},
		{name: "fresh-reservation", state: "live", updated: time.Now().Unix()},
		{name: "failed-row", state: "failed", updated: time.Now().Add(-time.Hour).Unix()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			seat := fleetdb.Seat{
				SeatID: "born", SessionID: "born", Engine: "cc", Backend: "herdr", State: tc.state,
				HerdrSocket: "/tmp/herdr.sock", HerdrWorkspace: "w1", UpdatedAt: tc.updated, LostNote: "kept",
			}
			if tc.state == "live" {
				seat.LostNote = ""
			}
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			got, err := retireDeadHerdrSeat(ctx, db, seat)
			if err != nil {
				t.Fatal(err)
			}
			stored, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil || !found {
				t.Fatalf("seat: found=%t err=%v", found, err)
			}
			retired := stored.State == "lost" && stored.LostNote == retireLostNote
			if retired != tc.retired || got != stored {
				t.Fatalf("stored %+v returned %+v (want retired=%t)", stored, got, tc.retired)
			}
		})
	}
}

// unreadableHostProc models a saved supervisor whose kernel birth cannot be
// read: unknown evidence, never positive death.
type unreadableHostProc struct {
	ProcFS
	pid int
}

func (proc unreadableHostProc) Stat(pid int) (ProcStat, error) {
	if pid == proc.pid {
		return ProcStat{}, fs.ErrPermission
	}
	return proc.ProcFS.Stat(pid)
}

func TestProvedListingRetiresDeadSeatOnly(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("read-only=%t", readOnly), func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "pfm.db")
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: dbPath})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			// The dead seat's old pane id now hosts a recycled occupant.
			socket, _ := herdrOwnershipSocket(
				t,
				`{"result":{"type":"pane_list","panes":[`+
					`{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"healthy","cwd":"/jail/healthy"},`+
					`{"pane_id":"w1:p2","workspace_id":"w1","terminal_id":"recycled","cwd":"/jail/recycled"},`+
					`{"pane_id":"w1:p3","workspace_id":"w1","terminal_id":"unknown","cwd":"/jail/unknown"}]}}`,
				"w1:p1",
			)
			seat := func(id, pane, terminal string, pid int) fleetdb.Seat {
				return fleetdb.Seat{
					SeatID: id, SessionID: id, Engine: "cc", Backend: "herdr", State: "live",
					HerdrSocket: socket, HerdrPane: pane, HerdrWorkspace: "w1", HerdrTerminalID: terminal,
					SeatHostPID: pid, SeatHostStart: 9,
				}
			}
			// Beside them: a seat ended while the listing reads it, and two seats
			// on a Herdr server that is gone, one whose supervisor died with it.
			gone := seat("gone", "w1:p4", "gone", 20)
			stopped := filepath.Join(testjail.ShortRoot(t), "stopped.sock")
			unanswered := func(id, pane string, pid int) fleetdb.Seat {
				s := seat(id, pane, id, pid)
				s.HerdrSocket = stopped
				return s
			}
			// And two births that reserved their row before layout.apply named a pane: one inside its
			// birth window, its seat-host already waiting for the pane; one long past it.
			reservation := func(id string, updated time.Time) fleetdb.Seat {
				s := seat(id, "", "", 0)
				s.SeatHostStart, s.UpdatedAt = 0, updated.Unix()
				return s
			}
			seats := []fleetdb.Seat{
				seat("healthy", "w1:p1", "healthy", 20),
				seat("dead", "w1:p2", "dead-terminal", 30),
				seat("unknown", "w1:p3", "unknown", 40),
				unanswered("stopped-dead", "w2:p1", 50),
				unanswered("stopped-alive", "w2:p2", 20),
				reservation("reserved", time.Now()),
				reservation("stale-reserved", time.Now().Add(-herdrBirthGrace-time.Minute)),
				// Its pane is closed and its supervisor has not exited yet.
				seat("closed", "w1:p6", "closed", 70),
			}
			for _, item := range append([]fleetdb.Seat{gone}, seats...) {
				if err := db.PutSeat(ctx, item); err != nil {
					t.Fatal(err)
				}
			}
			probed := probeHerdrSeatRows(ctx, append([]fleetdb.Seat{gone}, seats...))
			if err := db.DeleteSeat(ctx, gone.SeatID); err != nil {
				t.Fatal(err)
			}
			proc := unreadableHostProc{pid: 40, ProcFS: &fakeProcFS{
				processes: map[int]fakeProcess{
					10: {stat: ProcStat{ParentPID: 1, StartTime: 3}},
					20: {
						cmdline: []string{"pfm", "internal", "seat-host", "healthy"},
						stat:    ProcStat{ParentPID: 10, StartTime: 9},
					},
					60: {
						cmdline: []string{"pfm", "internal", "seat-host", "reserved"},
						stat:    ProcStat{ParentPID: 1, StartTime: 9},
					},
					70: {
						cmdline: []string{"pfm", "internal", "seat-host", "closed"},
						stat:    ProcStat{ParentPID: 1, StartTime: 9},
					},
				},
			}}
			proofDB := db
			if readOnly {
				proofDB = fleetdb.OpenSharedStateReadOnly(ctx, paths.Values{StateDB: dbPath})
				defer func() {
					if err := proofDB.Close(); err != nil {
						t.Error(err)
					}
				}()
			}
			rows := proveHerdrSeats(ctx, proofDB, proc, probed, readOnly)
			var listed []string
			for _, row := range rows {
				listed = append(listed, row.Seat.SeatID)
			}
			want := "healthy dead unknown stopped-dead stopped-alive stale-reserved closed"
			if strings.Join(listed, " ") != want {
				t.Fatalf("listed seats %q, want %q", listed, want)
			}
			want = "HERDR ERROR " + socket + ": seat stale-reserved has no saved supervisor birth; " +
				"absence unproved: old pane absence not explicitly acknowledged"
			if rows[5].Err == nil || rows[5].Err.Error() != want || rows[5].Seat.State != "live" {
				t.Fatalf("reservation past its birth window = %+v, want live with %q", rows[5], want)
			}
			want = "HERDR DEAD closed: pane closed; saved supervisor 70 has not exited"
			if rows[6].Err == nil || rows[6].Err.Error() != want || rows[6].Seat.State != "lost" {
				t.Fatalf("seat whose pane is closed under a running supervisor = %+v, want lost with %q", rows[6], want)
			}
			healthy, dead, unknown := rows[0], rows[1], rows[2]
			stoppedDead, stoppedAlive := rows[3], rows[4]
			if want := "HERDR DEAD stopped-dead: saved supervisor exited"; stoppedDead.Err == nil ||
				stoppedDead.Err.Error() != want || stoppedDead.Seat.State != "lost" {
				t.Fatalf("seat whose Herdr server and supervisor are gone = %+v, want lost with %q", stoppedDead, want)
			}
			if stoppedAlive.Err == nil || stoppedAlive.Seat.State != "live" ||
				!strings.HasPrefix(stoppedAlive.Err.Error(), "HERDR UNREACHABLE "+stopped+": ") {
				t.Fatalf(
					"seat on a silent socket with its supervisor alive = %+v, want live and unreachable",
					stoppedAlive,
				)
			}
			if healthy.Err != nil || healthy.Seat.State != "live" || healthy.CWD != "/jail/healthy" {
				t.Fatalf("healthy seat lost beside a dead one: %+v", healthy)
			}
			if dead.Err == nil || !strings.Contains(dead.Err.Error(), "HERDR DEAD dead") {
				t.Fatalf("dead seat diagnostic lost: %+v", dead)
			}
			if dead.Seat.State == "live" {
				t.Fatalf("positively dead seat still presents as live: %+v", dead.Seat)
			}
			if dead.CWD != "" || dead.Seat.HerdrTerminalID != "dead-terminal" || dead.Seat.HerdrPane != "w1:p2" {
				t.Fatalf("dead seat imported recycled pane metadata: %+v", dead)
			}
			if unknown.Err == nil || !strings.Contains(unknown.Err.Error(), "HERDR ERROR") ||
				unknown.Seat.State != "live" || unknown.CWD != "" {
				t.Fatalf("unknown ownership classified as dead or healthy: %+v", unknown)
			}
			for _, want := range seats {
				stored, found, err := db.Seat(ctx, want.SeatID)
				retired := (want.SeatID == "dead" || want.SeatID == "stopped-dead") && !readOnly
				if err != nil || !found || (stored.State == "lost") != retired || (stored.LostNote != "") != retired {
					t.Fatalf(
						"listing retire of %s (want retired=%t): %+v found=%t err=%v",
						want.SeatID, retired, stored, found, err,
					)
				}
			}
		})
	}
}

// The listing reads its seats, then one pane.list per socket, and each proof reads the seat's row and asks
// Herdr again: a pane born in between is one the snapshot never held, a pane closed in between is one
// Herdr no longer knows.
func TestProvedListingReadsAgainWhenItsSnapshotIsStale(t *testing.T) {
	const paneListed = `{"type":"pane_list","panes":[` +
		`{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"its-terminal","cwd":"/jail/chat"}]}`
	const paneNotListed = `{"type":"pane_list","panes":[]}`
	for _, test := range []struct {
		name     string
		snapshot string // what the listing read first
		socket   func(*testing.T) string
		host     int // the seat's saved supervisor
		want     string
	}{
		{
			name: "a pane born after the snapshot is proved", snapshot: paneNotListed,
			socket: func(t *testing.T) string {
				socket, _ := herdrOwnershipSocket(t, `{"result":`+paneListed+`}`, "w1:p1")
				return socket
			},
			want: "live <nil> cwd=/jail/chat terminal=its-terminal supervisor=20",
		},
		{
			name: "a pane closed after the snapshot is lost", snapshot: paneListed, host: 20,
			socket: func(t *testing.T) string {
				socket, _ := herdrRefusalSocket(t, `{"result":`+paneNotListed+`}`, retirePaneGone)
				return socket
			},
			want: "lost HERDR DEAD chat: pane closed; saved supervisor 20 has not exited cwd= terminal= supervisor=20",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			seat := fleetdb.Seat{
				SeatID: "chat", SessionID: "chat", Engine: "cc", Backend: "herdr", State: "live",
				HerdrSocket: test.socket(t), HerdrPane: "w1:p1", HerdrWorkspace: "w1", SeatHostPID: test.host,
			}
			if test.host != 0 {
				seat.SeatHostStart = 9
			}
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			proc := &fakeProcFS{processes: map[int]fakeProcess{
				10: {stat: ProcStat{ParentPID: 1, StartTime: 3}},
				20: {
					cmdline: []string{"pfm", "internal", "seat-host", "chat"},
					stat:    ProcStat{ParentPID: 10, StartTime: 9},
				},
			}}
			probed := probeHerdrSeatRows(ctx, []fleetdb.Seat{seat})
			probed[0].snapshot = json.RawMessage(test.snapshot)
			rows := proveHerdrSeats(ctx, db, proc, probed, false)
			if len(rows) != 1 {
				t.Fatalf("listed %d rows, want the one seat: %+v", len(rows), rows)
			}
			got := fmt.Sprintf(
				"%s %v cwd=%s terminal=%s supervisor=%d",
				rows[0].Seat.State, rows[0].Err, rows[0].CWD, rows[0].Seat.HerdrTerminalID, rows[0].Seat.SeatHostPID,
			)
			if got != test.want {
				t.Fatalf("listed as %q, want %q", got, test.want)
			}
		})
	}
}

func TestSettleHerdrProofJudgesAFailedProofByTheRowAfterIt(t *testing.T) {
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	seat := fleetdb.Seat{
		SeatID: "s", SessionID: "s", Engine: "cx", Backend: "herdr", State: "live",
		HerdrSocket: "/jail/herdr.sock", HerdrPane: "w1:p1", HerdrWorkspace: "w1", HerdrTerminalID: "t1",
	}
	stamped := seat
	stamped.SeatHostPID, stamped.SeatHostStart = 1460, 9
	taken := seat
	taken.Engine = "cc"
	refused := errors.New(
		`persist proved seat s: update seat "s": seat no longer exists or Herdr pane is already owned`,
	)
	for _, test := range []struct {
		name   string
		stored *fleetdb.Seat // the row after the first proof; nil: deleted during it
		first  error         // the first proof's failure; nil: it proved the seat
		proved []string      // the seats the proof ran on, by supervisor pid
		want   string
	}{
		{name: "a proved seat is proved once", stored: &seat, proved: []string{"0"}, want: "owned <nil>"},
		{
			name: "a failed proof of an unchanged row stays its failure", stored: &seat, first: refused,
			proved: []string{"0"}, want: "unowned " + refused.Error(),
		},
		{
			name: "a seat deleted during its proof is gone", first: refused,
			proved: []string{"0"}, want: "unowned seat s ownership changed or disappeared",
		},
		{
			name: "a seat taken by another engine during its proof is gone", stored: &taken, first: refused,
			proved: []string{"0"}, want: "unowned seat s ownership changed or disappeared",
		},
		{
			name: "a seat its birth stamped during the proof is proved again as it stands", stored: &stamped,
			first: refused, proved: []string{"0", "1460"}, want: "owned <nil>",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The listing proves the row as it read it; the change lands while the first proof runs.
			if err := errors.Join(db.DeleteSeat(ctx, seat.SeatID), db.PutSeat(ctx, seat)); err != nil {
				t.Fatal(err)
			}
			read, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil || !found {
				t.Fatalf("read back seat %s: found=%t %v", seat.SeatID, found, err)
			}
			switch {
			case test.stored == nil:
				err = db.DeleteSeat(ctx, seat.SeatID)
			case *test.stored != seat:
				err = db.PutSeat(ctx, *test.stored)
			}
			if err != nil {
				t.Fatal(err)
			}
			var proved []string
			_, owned, err := settleHerdrProof(ctx, db, read, func(on fleetdb.Seat) (fleetdb.Seat, bool, error) {
				proved = append(proved, fmt.Sprint(on.SeatHostPID))
				if len(proved) == 1 && test.first != nil {
					return on, false, test.first
				}
				return on, true, nil
			})
			got := map[bool]string{true: "owned", false: "unowned"}[owned] + " " + fmt.Sprint(err)
			if got != test.want || !slices.Equal(proved, test.proved) {
				t.Fatalf(
					"settled %q after proofs on supervisors %v, want %q after %v",
					got,
					proved,
					test.want,
					test.proved,
				)
			}
		})
	}
}
