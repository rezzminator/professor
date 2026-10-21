package kill

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHerdrGhostKillRequiresDeadSavedHost(t *testing.T) {
	for _, mode := range []string{"missing", "reused", "session", "unbound", "unreadable", "mismatch", "live-unowned"} {
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
			wantHide := mode == "missing" || mode == "reused" || mode == "session"
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
			if wantHide {
				if err != nil || !hidden || target.ID != seat.SessionID || target.SocketPath != "" ||
					target.PaneID != "" {
					t.Fatalf(
						"dead saved host must hide stable identity without stale address: target=%+v hidden=%t err=%v",
						target,
						hidden,
						err,
					)
				}
			} else if err == nil || hidden {
				t.Fatalf("unproved owner must be refused: target=%+v hidden=%t err=%v", target, hidden, err)
			}
			if len(spawner.args) != 0 {
				t.Fatalf("ghost target launched exit: %v", spawner.args)
			}
		})
	}
}
