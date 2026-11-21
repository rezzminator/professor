package hookentry

import (
	"bytes"
	"context"
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
