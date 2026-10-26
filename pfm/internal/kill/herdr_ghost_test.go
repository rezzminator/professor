package kill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHerdrGhostKillRequiresDeadSavedHost(t *testing.T) {
	for _, mode := range []string{
		"missing", "reused", "session", "unbound", "unreadable", "mismatch", "live-unowned",
		"unbound-retired", "unbound-retired-session", "unbound-retired-nopane",
		"unbound-supervisor-nopane", "unbound-probe-error-nopane", "unbound-live-nopane",
	} {
		t.Run(mode, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			seat := seedKillHerdr(t, database, server)
			request := Request{
				ID:         seat.SessionID,
				Engine:     pfmengine.ID(seat.Engine),
				SocketName: seat.HerdrSocket,
				PaneID:     "w9:p9",
			}
			wantHide := mode == "missing" || mode == "reused" || mode == "session" ||
				mode == "unbound-retired" || mode == "unbound-retired-session" || mode == "unbound-retired-nopane"
			switch mode {
			case "missing", "session", "mismatch":
				if err := os.RemoveAll(filepath.Join(server.proc, "90")); err != nil {
					t.Fatal(err)
				}
				if mode == "session" {
					request.SocketName, request.PaneID = "", ""
				}
				if mode == "mismatch" {
					request.Engine = pfmengine.Codex
				}
			case "reused":
				seat.SeatHostStart++
				if err := database.Shared().PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
			case "unbound":
				seat.SeatHostPID, seat.SeatHostStart = 0, 0
				if err := database.Shared().PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
			case "unbound-retired", "unbound-retired-session", "unbound-retired-nopane",
				"unbound-supervisor-nopane", "unbound-probe-error-nopane":
				// A pre-upgrade or never-bound row: no saved supervisor birth, and the
				// listing (HerdrSeatRetired) proves it retired only by a complete
				// Herdr listing without its pane plus no matching supervisor process.
				seat.SeatHostPID, seat.SeatHostStart = 0, 0
				if err := database.Shared().PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
				server.gone = true
				if mode != "unbound-supervisor-nopane" {
					if err := os.RemoveAll(filepath.Join(server.proc, "90")); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "unbound-probe-error-nopane" {
					server.probeError = "internal_error"
				}
				if mode == "unbound-retired-session" {
					request.SocketName, request.PaneID = "", ""
				}
				if mode != "unbound-retired" && mode != "unbound-retired-session" {
					// The CLI door: the resolved row carries the seat id and its Herdr
					// socket, but never bound a pane.
					request.ID, request.PaneID = seat.SeatID, ""
				}
			case "unbound-live-nopane":
				// A live never-bound chat: its pane is still listed, so the half
				// address proves the live owner and kills at its proved pane, never
				// hidden as a ghost and never by the stale request address.
				seat.SeatHostPID, seat.SeatHostStart = 0, 0
				if err := database.Shared().PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
				request.ID, request.PaneID = seat.SeatID, ""
			case "unreadable":
				if err := os.WriteFile(
					filepath.Join(server.proc, "90", "stat"),
					[]byte("malformed"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			spawner := &captureSpawner{}
			manager, err := New(database, Dependencies{Tmux: &fakeTmux{}, Spawner: spawner})
			if err != nil {
				t.Fatal(err)
			}
			target, err := manager.Kill(context.Background(), request)
			_, hidden, readErr := database.Killed(context.Background(), seat.SessionID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "unbound-live-nopane" {
				if err != nil || target.PaneID != seat.HerdrPane || target.SocketPath != seat.HerdrSocket ||
					target.SocketName != seat.SeatID {
					t.Fatalf("live unbound seat must be killed at its proved address: target=%+v err=%v", target, err)
				}
				return
			}
			switch {
			case wantHide:
				if err != nil || !hidden || target.ID != seat.SessionID || target.SocketPath != "" ||
					target.PaneID != "" {
					t.Fatalf(
						"dead saved host must hide stable identity without stale address: target=%+v hidden=%t err=%v",
						target,
						hidden,
						err,
					)
				}
			case err == nil || hidden:
				t.Fatalf("unproved owner must be refused: target=%+v hidden=%t err=%v", target, hidden, err)
			case strings.HasSuffix(mode, "-nopane") && strings.Contains(err.Error(), "requires socket and pane"):
				t.Fatalf("an unproved seat must be refused by its own proof, not the half-address guard: %v", err)
			}
			if len(spawner.args) != 0 {
				t.Fatalf("ghost target launched exit: %v", spawner.args)
			}
		})
	}
}

// A native kill whose seat row vanished mid-kill takes the same retirement proof
// as the listing: an unbound seat is gone only when Herdr lists no such pane and
// no matching supervisor runs, and a still-listed pane keeps it refused.
func TestHerdrProveVanishedUnboundSeat(t *testing.T) {
	for _, listed := range []bool{false, true} {
		jail := newKillJail(t)
		database := jail.open(t)
		server := newKillHerdr(t)
		seat := seedKillHerdr(t, database, server)
		seat.SeatHostPID, seat.SeatHostStart = 0, 0
		if !listed {
			server.gone = true
			if err := os.RemoveAll(filepath.Join(server.proc, "90")); err != nil {
				t.Fatal(err)
			}
		}
		if err := database.Shared().DeleteSeat(context.Background(), seat.SeatID); err != nil {
			t.Fatal(err)
		}
		_, owned, err := (herdrKiller{database: database, seat: seat}).prove(context.Background())
		if owned || (err == nil) == listed {
			t.Fatalf("listed=%t: owned=%t err=%v, want retired without error only when unlisted", listed, owned, err)
		}
	}
}
