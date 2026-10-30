package gather

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// herdrRefusalSocket answers pane.list with list and pane.get with get, and
// counts the pane.get requests it answered.
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
		// A birth reserves its row before layout.apply names the pane or the
		// seat-host exists: the empty address proves nothing yet.
		{name: "birth-reservation", get: retirePaneGone, proc: absent, dead: true, pending: func(seat *fleetdb.Seat) {
			seat.HerdrPane = ""
			seat.UpdatedAt = time.Now().Unix()
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
			rows := proveHerdrSeats(ctx, db, tc.proc, probeHerdrSeatRows(ctx, []fleetdb.Seat{seat}), false)
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
