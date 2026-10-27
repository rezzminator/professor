package reload

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRunHerdrProvesSupervisorBeforePublication(t *testing.T) {
	for _, mode := range []string{"recycled", "moved", "moved-explicit", "moved-durable", "unknown-host"} {
		t.Run(mode, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			if mode == "recycled" {
				f.terminal = "unrelated-terminal"
			}
			if strings.HasPrefix(mode, "moved") {
				f.canonical = "w9:p2"
			}
			if mode == "moved-explicit" {
				f.request.Pane = f.canonical
			}
			if mode == "moved-durable" {
				before := f.seat
				after := before
				after.HerdrPane = f.canonical
				after.HerdrWorkspace = "w9"
				if err := f.db.UpdateSeat(context.Background(), before, after); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unknown-host" {
				values, err := paths.Resolve()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(values.ProcRoot, "90", "cmdline"),
					[]byte("claude\x00"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			_, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
			if strings.HasPrefix(mode, "moved") {
				if err != nil {
					t.Fatalf("moved owned reload failed: %v", err)
				}
				row, found, e := f.db.Seat(context.Background(), f.seat.SeatID)
				if e != nil || !found || row.HerdrPane != f.canonical || row.HerdrWorkspace != "w9" {
					t.Fatalf("canonical row=%+v err=%v", row, e)
				}
				return
			}
			if err == nil {
				t.Fatalf("unproved %s occupant accepted; submitted=%t", mode, f.submitted)
			}
			run, e := f.db.ReadSeatRun(f.seat.SeatID)
			if e != nil || !reflect.DeepEqual(run, f.old) {
				t.Fatalf("unproved reload changed recipe: %+v %v", run, e)
			}
		})
	}
}

func TestRunHerdrRechecksSupervisorBeforeDraftMutation(t *testing.T) {
	f := newReloadHerdrFixture(t)
	f.text = "❯ parked draft"
	f.onRead = func() { f.terminal = "recycled-during-ready" }
	_, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
	if err == nil || len(f.keys) != 0 || len(f.typed) != 0 {
		t.Fatalf("unproved draft mutation keys=%v typed=%v err=%v", f.keys, f.typed, err)
	}
}

func TestRunHerdrTracksMoveDuringReadiness(t *testing.T) {
	f := newReloadHerdrFixture(t)
	f.onRead = func() { f.canonical = "w9:p2" }
	_, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
	if err != nil {
		t.Fatalf("live supervisor move during readiness failed: %v", err)
	}
	row, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
	if err != nil || !found || row.HerdrPane != "w9:p2" || !f.submitted {
		t.Fatalf("moved row=%+v submitted=%t err=%v", row, f.submitted, err)
	}
}

func TestRunHerdrLocksSeatBeforeSessionDiscovery(t *testing.T) {
	f := newReloadHerdrFixture(t)
	before := f.seat
	f.seat.SessionID = ""
	f.request.SessionID = ""
	if err := f.db.UpdateSeat(context.Background(), before, f.seat); err != nil {
		t.Fatal(err)
	}
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	f.onRead = func() {
		if checked {
			return
		}
		checked = true
		lockctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		defer cancel()
		unlock, err := fleetdb.LockSeat(lockctx, values, f.seat.SeatID)
		if err == nil {
			if err := unlock(); err != nil {
				t.Error(err)
			}
			t.Error("readiness ran without the pre-session SeatID lock")
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("lock probe: %v", err)
		}
	}
	result, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
	if err != nil || !checked || !result.New || !f.submitted {
		t.Fatalf("pending session reload result=%+v submitted=%t err=%v", result, f.submitted, err)
	}
}

func TestRunHerdrTracksMoveDuringStartup(t *testing.T) {
	f := newReloadHerdrFixture(t)
	f.onExit = func() { f.canonical = "w9:p2" }
	_, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
	if err != nil {
		t.Fatalf("live supervisor move during startup failed: %v", err)
	}
	row, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
	if err != nil || !found || row.HerdrPane != "w9:p2" || row.ReloadPending {
		t.Fatalf("moved row=%+v err=%v", row, err)
	}
}

// TestRunHerdrNamesAnEndedChatApartFromADeadSupervisor: a chat ended while the
// reload waited for it to go idle says so; only a supervisor that died under a
// live seat is reported dead.
func TestRunHerdrNamesAnEndedChatApartFromADeadSupervisor(t *testing.T) {
	for _, test := range []struct{ mode, want string }{
		{"end pending", "native reload: the chat was ended while reload waited"},
		{"seat retired", "native reload: the chat was ended while reload waited"},
		{"supervisor dead", "native reload supervisor is dead"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			fired := false
			f.onRead = func() {
				if fired {
					return
				}
				fired = true
				ctx := context.Background()
				switch test.mode {
				case "end pending":
					row, found, err := f.db.Seat(ctx, f.seat.SeatID)
					if err != nil || !found {
						t.Errorf("read seat: found=%v err=%v", found, err)
						return
					}
					ending := row
					ending.EndPending = true
					if err := f.db.UpdateSeat(ctx, row, ending); err != nil {
						t.Error(err)
					}
				case "seat retired":
					if err := f.db.DeleteSeat(ctx, f.seat.SeatID); err != nil {
						t.Error(err)
					}
				}
				if err := os.RemoveAll(filepath.Join(values.ProcRoot, "90")); err != nil {
					t.Error(err)
				}
			}
			_, err = RunHerdr(context.Background(), f.request, f.options, io.Discard)
			if !fired || err == nil || !strings.Contains(err.Error(), test.want) || f.submitted {
				t.Fatalf("fired=%v submitted=%v err=%v, want %q", fired, f.submitted, err, test.want)
			}
		})
	}
}
