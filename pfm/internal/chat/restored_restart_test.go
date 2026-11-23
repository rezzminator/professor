package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestRestartJudgesAHerdrSeatsResumeAndItsReturn: pfm restart brings a Herdr
// chat back only when its seat can resume (present, no end pending, a saved
// run recipe), and counts it back only once its seat reads live on the
// restarted server, no end pending, with a seat host that is running and is
// not the one the seat named at plan time.
func TestRestartJudgesAHerdrSeatsResumeAndItsReturn(t *testing.T) {
	testjail.Fleet(t)
	runtime, err := pfmconfig.RuntimeOrDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	values := runtime.Paths
	socket := filepath.Join(t.TempDir(), "herdr.sock")
	// pid 77 runs, born at tick 100; pid 78 does not.
	host := filepath.Join(values.ProcRoot, "77")
	if err := os.MkdirAll(host, 0o700); err != nil {
		t.Fatal(err)
	}
	stat := "77 (pfm) S 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100"
	if err := os.WriteFile(filepath.Join(host, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	db := fleetdb.OpenSharedState(ctx, values)
	for index, seat := range []fleetdb.Seat{
		{SeatID: "seat-end", State: "live", EndPending: true, SeatHostPID: 77, SeatHostStart: 100},
		{SeatID: "seat-norecipe", State: "live"},
		{SeatID: "seat-ok", State: "live", SeatHostPID: 77, SeatHostStart: 100},
		{SeatID: "seat-lost", State: "lost", SeatHostPID: 77, SeatHostStart: 100},
		{SeatID: "seat-other", State: "live", SeatHostPID: 77, SeatHostStart: 100, HerdrSocket: "other.sock"},
		{SeatID: "seat-nohost", State: "live"},
		{SeatID: "seat-gone", State: "live", SeatHostPID: 78, SeatHostStart: 100},
		{SeatID: "seat-reborn", State: "live", SeatHostPID: 77, SeatHostStart: 99},
	} {
		seat.SessionID, seat.Engine, seat.Backend = "s-"+seat.SeatID, "cc", "herdr"
		seat.HerdrPane = fmt.Sprintf("w1:p%d", index+1)
		if seat.HerdrSocket == "" {
			seat.HerdrSocket = socket
		}
		if err := db.PutSeat(ctx, seat); err != nil {
			t.Fatal(err)
		}
		if seat.SeatID == "seat-norecipe" {
			continue
		}
		if err := db.WriteSeatRun(
			seat.SeatID,
			fleetdb.SeatRun{Binary: "claude", Args: []string{"--resume"}},
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// The seat host every seat named at plan time, unless a row says otherwise.
	planned := RestartSeatHost{PID: 76, Start: 50}
	running := RestartSeatHost{PID: 77, Start: 100}
	for _, row := range []struct {
		seat   string
		why    string
		host   RestartSeatHost
		before *RestartSeatHost
		back   bool
		seen   string
	}{
		{
			seat: "seat-missing", why: "its seat seat-missing is not in pfm's state",
			seen: "seat seat-missing is gone from pfm's state",
		},
		{
			seat: "seat-end", why: "its seat seat-end has an end pending", host: running,
			seen: "seat seat-end has an end pending",
		},
		{
			seat: "seat-norecipe", why: "its seat seat-norecipe has no saved run recipe to resume from",
			seen: "seat seat-norecipe has no seat host yet",
		},
		{seat: "seat-ok", host: running, back: true, seen: "seat seat-ok live in Herdr pane w1:p3, seat host pid 77"},
		{
			seat: "seat-ok", host: running, before: &running,
			seen: "seat seat-ok still names seat host pid 77 from before the restart; no resume has started a new one",
		},
		{seat: "seat-lost", host: running, seen: "seat seat-lost reads lost"},
		{seat: "seat-other", host: running, seen: "seat seat-other is on Herdr server other.sock, not " + socket},
		{seat: "seat-nohost", seen: "seat seat-nohost has no seat host yet"},
		{
			seat: "seat-gone", host: RestartSeatHost{PID: 78, Start: 100},
			seen: "seat seat-gone still names seat host pid 78, which is gone; no resume has started a new one",
		},
		{
			seat: "seat-reborn", host: RestartSeatHost{PID: 77, Start: 99},
			seen: "seat seat-reborn still names seat host pid 77, which is gone; no resume has started a new one",
		},
	} {
		chat := headless.Chat{ID: "s-" + row.seat, SeatID: row.seat, Backend: "herdr", Socket: socket}
		why, host, err := RestartRestorable(ctx, chat, values)
		if err != nil || why != row.why || host != row.host {
			t.Fatalf("%s restorable: %q, host %+v, %v; want %q, host %+v", row.seat, why, host, err, row.why, row.host)
		}
		before := planned
		if row.before != nil {
			before = *row.before
		}
		back, seen, err := RestartBack(ctx, chat, socket, before, values)
		if err != nil || back != row.back || seen != row.seen {
			t.Fatalf("%s back after host %+v: %v %q, %v; want %v %q",
				row.seat, before, back, seen, err, row.back, row.seen)
		}
	}
}
