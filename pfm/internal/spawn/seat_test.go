package spawn

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRecordTmuxSeatRefusesEscapingSocket(t *testing.T) {
	if err := RecordTmuxSeat(
		context.Background(),
		paths.Values{TmuxDir: t.TempDir()},
		fleetdb.Seat{SessionID: "sid", TmuxSocket: "../foreign"},
	); err == nil {
		t.Fatal("foreign socket was accepted")
	}
}

func TestRecordTmuxSeatUsesCurrentClock(t *testing.T) {
	oldClock := clock.Real
	clock.Real = clock.NewFake(time.Unix(1700000000, 0))
	t.Cleanup(func() { clock.Real = oldClock })
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "pfm.db"), TmuxDir: filepath.Join(root, "tmux")}
	if err := RecordTmuxSeat(context.Background(), values, fleetdb.Seat{
		SeatID: "cc-test", SessionID: "sid", TmuxSocket: "cc-test", Engine: "cc",
	}); err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = state.Close() }()
	seat, found, err := state.Seat(context.Background(), "cc-test")
	if err != nil || !found || seat.UpdatedAt != 1700000000 {
		t.Fatalf("seat=%+v found=%v err=%v, want updated_at 1700000000", seat, found, err)
	}
}
