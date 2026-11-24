package gather

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrRestorePending(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name          string
		serverStarted time.Time // zero: no socket file
		seatWritten   time.Time
		want          bool
	}{
		{"pane the server restored, launch not started", now.Add(-10 * time.Second), now.Add(-time.Hour), true},
		{"server start just inside the window", now.Add(-herdrBirthGrace + 5*time.Second), now.Add(-time.Hour), true},
		{"server start just past the window", now.Add(-herdrBirthGrace - 5*time.Second), now.Add(-time.Hour), false},
		{"seat written after the server start", now.Add(-10 * time.Second), now.Add(-5 * time.Second), false},
		{"socket file unreadable", time.Time{}, now.Add(-time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
			if !tc.serverStarted.IsZero() {
				if err := os.WriteFile(socket, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(socket, tc.serverStarted, tc.serverStarted); err != nil {
					t.Fatal(err)
				}
			}
			seat := fleetdb.Seat{SeatID: "seat-restored", HerdrSocket: socket, UpdatedAt: tc.seatWritten.Unix()}
			if got := HerdrRestorePending(seat); got != tc.want {
				t.Fatalf("HerdrRestorePending(server started %s, seat written %s) = %t, want %t (socket %s)",
					tc.serverStarted, tc.seatWritten, got, tc.want, socket)
			}
		})
	}
}
