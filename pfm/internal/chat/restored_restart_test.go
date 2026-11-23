package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
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
	// Boot at 999980 s: pid 79 (tick 2000) was born at 1000000, pid 80 (tick
	// 1000) at 999990, pid 81 (tick 1100) at 999991; the restart reads at 1000000.
	if err := os.WriteFile(filepath.Join(values.ProcRoot, "stat"), []byte("cpu 0\nbtime 999980\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for pid, tick := range map[int]int{79: 2000, 80: 1000, 81: 1100} {
		dir := filepath.Join(values.ProcRoot, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf("%d (pfm) S 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 %d", pid, tick)
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	timing := clock.NewFake(time.Unix(1000000, 0))
	// The restarted Herdr server started at 999995: its socket's mtime.
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(socket, time.Unix(999995, 0), time.Unix(999995, 0)); err != nil {
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
		{
			SeatID: "seat-failed", State: "failed", SeatHostPID: 77, SeatHostStart: 100,
			LostNote:  "the engine exited 1 after 300ms, inside its start window: Error: fixture MCP config missing",
			UpdatedAt: 999995,
		},
		{SeatID: "seat-young", State: "live", SeatHostPID: 79, SeatHostStart: 2000, SeatHostBirth: 1000000},
		{SeatID: "seat-settled", State: "live", SeatHostPID: 80, SeatHostStart: 1000, SeatHostBirth: 999990},
		{SeatID: "seat-nearly", State: "live", SeatHostPID: 81, SeatHostStart: 1100, SeatHostBirth: 999991},
		{
			SeatID: "seat-failed-before", State: "failed", SeatHostPID: 77, SeatHostStart: 100,
			LostNote: "an earlier start failed", UpdatedAt: 999994,
		},
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
		err    string
	}{
		{
			seat: "seat-missing", why: "its seat seat-missing is not in pfm's state",
			seen: "seat seat-missing is gone from pfm's state",
		},
		{
			seat: "seat-end", why: "its seat seat-end has an end pending", host: running, before: &running,
			seen: "seat seat-end has an end pending",
		},
		{
			seat: "seat-end", why: "its seat seat-end has an end pending", host: running,
			err: "its restored launch failed: seat seat-end is ending: its restored engine under seat host pid 77 exited",
		},
		{
			seat: "seat-failed", host: running,
			err: "its restored launch failed: seat seat-failed: the engine exited 1 after 300ms, inside its start " +
				"window: Error: fixture MCP config missing",
		},
		{
			seat: "seat-failed-before", host: running,
			seen: "seat seat-failed-before reads failed from before the Herdr server start",
		},
		{
			seat: "seat-young", host: RestartSeatHost{PID: 79, Start: 2000, Birth: 1000000},
			seen: "seat seat-young live in Herdr pane w1:p10 under seat host pid 79 for 0s, inside its 10s start window",
		},
		{
			seat: "seat-nearly", host: RestartSeatHost{PID: 81, Start: 1100, Birth: 999991},
			seen: "seat seat-nearly live in Herdr pane w1:p12 under seat host pid 81 for 9s, inside its 10s start window",
		},
		{
			seat: "seat-settled", host: RestartSeatHost{PID: 80, Start: 1000, Birth: 999990}, back: true,
			seen: "seat seat-settled live in Herdr pane w1:p11, seat host pid 80",
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
		back, seen, err := RestartBack(ctx, chat, socket, before, values, timing)
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if gotErr != row.err || (err != nil) != errors.Is(err, ErrRestartFailed) || back != row.back ||
			seen != row.seen {
			t.Fatalf("%s back after host %+v: %v %q, %q; want %v %q, %q",
				row.seat, before, back, seen, gotErr, row.back, row.seen, row.err)
		}
	}
}
