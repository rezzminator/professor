package kill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// stageHeldEngine has herdr-keep hold server's pane: holder h1 (pid 80) runs
// shell 90, whose terminal's foreground group is the engine's, 7.
func stageHeldEngine(t *testing.T, server *killHerdr) {
	t.Helper()
	for name, body := range map[string]string{
		"80/stat":   "80 (herdr-keep) S 1 80 80 0 0 " + strings.Repeat("0 ", 13) + "8 0\n",
		"90/stat":   "90 (pfm) S 80 90 90 0 7 " + strings.Repeat("0 ", 13) + "9 0\n",
		"7/stat":    "7 (claude) S 90 7 90 0 7 " + strings.Repeat("0 ", 13) + "7 0\n",
		"7/cmdline": pfmengine.MustLookup(pfmengine.Claude).Binary + "\x00",
	} {
		path := filepath.Join(server.proc, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testjail.StageHerdrKeep(t, testjail.HerdrKeepListing(80, 8, 90, 9, "w1:p1", server.socket), 0)
}

// Closing a held pane ends only herdr-keep's attach client: the kill is done
// when the holder's shell has ended, and a shell that outlives the close keeps
// the seat and names the cleanup.
func TestHerdrKillOfAHeldPaneAwaitsItsShell(t *testing.T) {
	for _, test := range []struct {
		name     string
		survives bool
		want     string
	}{
		{name: "shell ends"},
		{
			name: "shell survives", survives: true,
			want: "closed Herdr pane w1:p1, but herdr-keep holder h1 still runs its shell (pid 90): " +
				"end it with `herdr-keep kill h1`; seat seat-kill retained",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			server.processes = []any{map[string]any{"pid": 77, "name": "herdr-keep"}}
			server.closeKeepsShell = test.survives
			stageHeldEngine(t, server)
			seat := seedKillHerdr(t, database, server)
			if err := database.Shared().WriteSeatRun(
				seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/claude", CWD: jail.root},
			); err != nil {
				t.Fatal(err)
			}
			client := herdrKiller{
				engine: pfmengine.Claude, database: database, seat: seat, sidDir: jail.sidDir,
				heldShellWait: time.Millisecond,
			}
			err := client.KillPane(context.Background(), seat.HerdrSocket, seat.HerdrPane)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != test.want {
				t.Fatalf("KillPane error = %q, want %q", got, test.want)
			}
			server.mu.Lock()
			closed := server.closed
			server.mu.Unlock()
			if !closed {
				t.Fatal("pane.close never reached Herdr")
			}
			_, found, readErr := database.Shared().Seat(context.Background(), seat.SeatID)
			if readErr != nil || found != test.survives {
				t.Fatalf("seat row found = %t (%v), want %t", found, readErr, test.survives)
			}
			if _, err := database.Shared().ReadSeatRun(seat.SeatID); (err == nil) != test.survives {
				t.Fatalf("seat recipe read = %v, want kept %t", err, test.survives)
			}
		})
	}
}
