package hookentry

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

func TestHerdrTmuxSeatLifecycle(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	seat := fleetdb.Seat{SeatID: "cc-native-test", SessionID: "old", Engine: "cc", Backend: "tmux", State: "live"}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	reporter := herdrReporter{
		options: herdrOptions{engine: "cc", event: herdr.EventStart},
		deps: herdrDeps{seatPaths: &values, getenv: func(key string) string {
			if key == "TMUX" {
				return filepath.Join(root, seat.SeatID) + ",12,0"
			}
			return ""
		}},
	}
	if err := reporter.tmuxSeatLifecycle(context.Background(), herdrPayload{SessionID: "bound"}); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.Seat(context.Background(), seat.SeatID)
	if err != nil || !found || got.SessionID != "bound" {
		t.Fatalf("tmux binding=%+v found=%v err=%v", got, found, err)
	}
	// A reload's own /exit ends the engine it respawns on the same seat: while the reload holds the
	// pane, the session end keeps the seat.
	reporter.options.event, reporter.tmuxPane, reporter.deps.sidDir = herdr.EventEnd, "%0", root
	lock, err := os.OpenFile(reload.LockPath(root, seat.SeatID, "%0"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	exit := herdrPayload{SessionID: "bound", Reason: "prompt_input_exit"}
	if err := reporter.tmuxSeatLifecycle(context.Background(), exit); err != nil {
		t.Fatal(err)
	}
	if got, found, err := db.Seat(context.Background(), seat.SeatID); err != nil || !found || got.SessionID != "bound" {
		t.Fatalf("tmux seat after a reload's /exit = %+v found=%v err=%v, want it kept bound", got, found, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reporter.tmuxSeatLifecycle(context.Background(), exit); err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.Seat(context.Background(), seat.SeatID); err != nil || found {
		t.Fatalf("ended tmux seat found=%v err=%v", found, err)
	}
}
