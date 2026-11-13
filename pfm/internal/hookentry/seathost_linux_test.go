//go:build linux

package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type seatFixtureReport struct {
	Argv0                             string
	PID, Group, Foreground            int
	Pane, Socket, Session             string
	SupervisorPID                     int
	SupervisorCWD                     string
	LedgerMarker, LedgerPID, ArmCalls string
	LedgerArmed                       bool
	HostStart                         uint64
	HostBirth                         int64
	Terminal                          string
	StdoutTTY, StderrTTY              bool
	Devices                           [3]uint64
}

func waitSeatFixture(t *testing.T, done <-chan error, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("supervisor exited before expected transition: %v", err)
		case <-deadline.C:
			t.Fatal("supervisor transition timeout")
		case <-tick.C:
			if ready() {
				return
			}
		}
	}
}

func TestSeatHostHangupPreservesRestorableSeat(t *testing.T) {
	ctx := context.Background()
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	seat := fleetdb.Seat{
		SeatID: "interrupted", SessionID: "session", Backend: "herdr", State: "live",
		HerdrSocket: "/fixture/stopped.sock", HerdrPane: "w6:p3",
	}
	if err := db.PutSeat(ctx, seat); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/engine"}); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if status := closeSeat(ctx, db, seat, 129, &stderr); status != 129 || stderr.Len() != 0 {
		t.Fatalf("hangup status=%d stderr=%s", status, &stderr)
	}
	if _, found, err := db.Seat(ctx, seat.SeatID); err != nil || !found {
		t.Fatalf("hangup discarded restoration owner: found=%t err=%v", found, err)
	}
	if _, err := db.ReadSeatRun(seat.SeatID); err != nil {
		t.Fatalf("hangup discarded restoration recipe: %v", err)
	}
}

func TestSeatHostDoesNotCloseAnAbsentSeat(t *testing.T) {
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var stderr bytes.Buffer
	status := closeSeat(
		context.Background(),
		db,
		fleetdb.Seat{SeatID: "removed", HerdrSocket: "/fixture/unowned.sock", HerdrPane: "w6:p3"},
		0,
		&stderr,
	)
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("absent seat attempted stale close: status=%d stderr=%s", status, &stderr)
	}
}

// A seat whose end cannot be scheduled keeps its owner row and its resume recipe. A Herdr that
// refuses because it is shutting down is a hangup (129, restoration, nothing printed): macOS ends
// the engine before the terminal hangs up. Any other failure is the hook's error.
func TestSeatHostCloseFailureKeepsOwnerAndRecipe(t *testing.T) {
	stopping := filepath.Join(t.TempDir(), "stopping.sock")
	listener, err := net.Listen("unix", stopping)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	t.Cleanup(func() { _ = listener.Close(); <-served })
	go func() {
		defer close(served)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var request struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(conn).Decode(&request); err != nil {
				t.Error(err)
			}
			const refusal = `{"id":%q,"error":{"code":"server_unavailable","message":"server is shutting down"}}`
			fmt.Fprintf(conn, refusal+"\n", request.ID)
			_ = conn.Close()
		}
	}()
	// A crashed Herdr server's socket file, nothing listening: the end cannot be proved.
	crashed := filepath.Join(t.TempDir(), "crashed.sock")
	crashedListener, err := net.Listen("unix", crashed)
	if err != nil {
		t.Fatal(err)
	}
	crashedListener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := crashedListener.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		socket     string
		wantCode   int
		wantStderr string
	}{
		{
			"no Herdr server", crashed, 1,
			"pfm internal seat-host: schedule ended seat: prove native end: prove seat pending-close pane.list: " +
				"herdr socket " + crashed + ": dial for pane.list pfm-seat: " +
				"dial unix " + crashed + ": connect: connection refused\n",
		},
		{"Herdr shutting down", stopping, 129, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() { _ = db.Close() }()
			seat := fleetdb.Seat{
				SeatID:      "pending-close",
				SessionID:   "session",
				Backend:     "herdr",
				State:       "live",
				HerdrSocket: tc.socket,
				HerdrPane:   "w1:p1",
			}
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/engine"}); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			code := closeSeat(context.Background(), db, seat, 0, &stderr, config.Runtime{Paths: values})
			_, found, err := db.Seat(context.Background(), seat.SeatID)
			_, recipeErr := db.ReadSeatRun(seat.SeatID)
			if code != tc.wantCode || stderr.String() != tc.wantStderr || !found || err != nil || recipeErr != nil {
				t.Fatalf(
					"closeSeat = %d, stderr %q, seat found=%t err=%v, recipe err=%v; want %d, stderr %q, both kept",
					code, stderr.String(), found, err, recipeErr, tc.wantCode, tc.wantStderr,
				)
			}
		})
	}
}

func TestFailedSeatStartDoesNotOverwriteAReplacementSupervisor(t *testing.T) {
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "state.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	old := fleetdb.Seat{
		SeatID:        "rebound",
		Backend:       "herdr",
		State:         "live",
		SeatHostPID:   100,
		SeatHostStart: 1,
		SeatHostBirth: 1,
	}
	next := old
	next.SeatHostPID = 101
	next.SeatHostStart = 2
	next.SeatHostBirth = 2
	if err := db.PutSeat(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	failedSeatStart(context.Background(), db, old, fmt.Errorf("old startup failed"), &stderr)
	current, found, err := db.Seat(context.Background(), old.SeatID)
	if err != nil || !found || current != next ||
		stderr.String() != `pfm internal seat-host: seat rebound: old startup failed
pfm internal seat-host: failed supervisor binding changed
` {
		t.Fatalf(
			"replacement changed: before=%+v after=%+v found=%v err=%v stderr=%s",
			next,
			current,
			found,
			err,
			&stderr,
		)
	}
}
