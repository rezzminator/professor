package hookentry

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestSeatEngineEnded pins what an engine's end does to its seat: a reload
// awaited after a clean run restarts with the stored seat, anything else goes
// to closeSeat, a hangup keeping the seat for restoration.
func TestSeatEngineEnded(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stored        bool
		reloadPending bool
		status        int
		faulted       bool
		wantReloading bool
		wantCode      int
		otherHost     bool
		wantStderr    string
	}{
		{name: "reload after a clean run restarts", stored: true, reloadPending: true, wantReloading: true},
		{
			name: "reload after a faulted run closes", stored: true, reloadPending: true, status: 129, faulted: true,
			wantCode: 129,
		},
		{name: "hangup keeps the seat", stored: true, status: 129, wantCode: 129},
		{name: "absent seat ends with the engine's status", status: 7, wantCode: 7},
		{
			name: "another supervisor's seat stays", stored: true, status: 7, otherHost: true, wantCode: 1,
			wantStderr: "pfm internal seat-host: ended supervisor binding changed\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
			db := fleetdb.OpenSharedState(ctx, values)
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			seat := fleetdb.Seat{
				SeatID: "ended", SessionID: "session", Backend: "herdr", State: "live",
				HerdrSocket: "/fixture/owner.sock", HerdrPane: "w6:p3", ReloadPending: tc.reloadPending,
				SeatHostPID: 4242, SeatHostStart: 7,
			}
			stored := seat
			if tc.otherHost {
				stored.SeatHostPID = 4343
			}
			if tc.stored {
				if err := db.PutSeat(ctx, stored); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			next, reloading, code := seatEngineEnded(ctx, db, seat, tc.status, tc.faulted, &stderr,
				config.Runtime{Paths: values})
			read, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			if reloading != tc.wantReloading || code != tc.wantCode || stderr.String() != tc.wantStderr ||
				found != tc.stored || (tc.stored && read != stored) || (tc.wantReloading && next != read) ||
				(!tc.wantReloading && next != seat) {
				t.Fatalf(
					"seatEngineEnded = %+v %t %d, stderr %q, stored %+v found=%t; want reloading %t code %d, stderr %q, seat %+v kept=%t",
					next,
					reloading,
					code,
					&stderr,
					read,
					found,
					tc.wantReloading,
					tc.wantCode,
					tc.wantStderr,
					stored,
					tc.stored,
				)
			}
		})
	}
}

// TestFailedSeatNoteKeepsTheRestoreNote: a failed start's note leads with its
// cause and keeps the note the seat held before (a restore's loss note), the
// same one across repeated failures; a start that succeeds keeps only that.
func TestFailedSeatNoteKeepsTheRestoreNote(t *testing.T) {
	restore := "your last message was not answered — re-send it"
	first := "the engine exited 1 after 300ms, inside its 10s start window: Error: fixture"
	for _, row := range []struct {
		name    string
		seat    fleetdb.Seat
		cause   string
		failed  string
		started string
	}{
		{
			name: "restored seat fails", seat: fleetdb.Seat{State: "lost", LostNote: restore}, cause: first,
			failed: first + " — before the failed start: " + restore, started: restore,
		},
		{
			name:  "failed seat fails again",
			seat:  fleetdb.Seat{State: "failed", LostNote: first + " — before the failed start: " + restore},
			cause: "open pane terminal: fixture", failed: "open pane terminal: fixture — before the failed start: " + restore,
			started: restore,
		},
		{
			name: "seat with no note fails", seat: fleetdb.Seat{State: "live"}, cause: first, failed: first,
		},
		{
			name: "failed seat with no kept note fails again", seat: fleetdb.Seat{State: "failed", LostNote: first},
			cause: "retry failed: fixture", failed: "retry failed: fixture",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			failed := failedSeatNote(errors.New(row.cause), row.seat)
			if failed != row.failed {
				t.Fatalf("failedSeatNote = %q, want %q", failed, row.failed)
			}
			if started := startedSeatNote(failed); started != row.started {
				t.Fatalf("startedSeatNote(%q) = %q, want %q", failed, started, row.started)
			}
		})
	}
}
