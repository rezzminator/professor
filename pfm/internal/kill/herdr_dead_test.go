package kill

import (
	"context"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// A picker row of a lost native seat still carries its Herdr address: with no
// live seat behind it the kill hides the chat and ends nothing, never reading
// the Herdr socket path as a tmux socket name. Named by its session (the
// picker's own kill) or by its seat (resolvedKillRequest, the CLI and MCP
// kill), the hide lands on the session id the row is keyed on. When the chat
// was resumed into a new live seat, the kill named by the lost seat ends that
// seat with the hide, as a kill named by the session does: a hidden chat never
// keeps running.
func TestHerdrKillOfALostSeatOnlyHides(t *testing.T) {
	for _, named := range []string{"session", "seat", "seat-resumed"} {
		t.Run(named, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			seat := seedKillHerdr(t, database, server)
			lost := seat
			lost.State = "lost"
			if named == "seat-resumed" {
				// The lost seat is an older one of the same chat; seat-kill runs it now.
				lost.SeatID, lost.HerdrPane, lost.HerdrTerminalID = "seat-lost", "w1:p7", "lost-terminal"
				if err := database.Shared().PutSeat(context.Background(), lost); err != nil {
					t.Fatal(err)
				}
				resumed := seat
				resumed.UpdatedAt = 2
				if err := database.Shared().UpdateSeat(context.Background(), seat, resumed); err != nil {
					t.Fatal(err)
				}
			} else if err := database.Shared().UpdateSeat(context.Background(), seat, lost); err != nil {
				t.Fatal(err)
			}
			spawner := &captureSpawner{}
			manager, err := New(database, Dependencies{Paths: jail.resolved(), Tmux: &fakeTmux{}, Spawner: spawner})
			if err != nil {
				t.Fatal(err)
			}
			request := Request{
				ID:         lost.SessionID,
				Engine:     pfmengine.ID(lost.Engine),
				SocketName: lost.HerdrSocket,
				PaneID:     lost.HerdrPane,
			}
			if named != "session" {
				request.ID = lost.SeatID
			}
			target, err := manager.Kill(context.Background(), request)
			want, spawns := Target{ID: "native-session"}, 0
			if named == "seat-resumed" {
				want.SocketPath, want.PaneID, spawns = server.socket, "w1:p1", 1
			}
			if err != nil || target.ID != want.ID || target.SocketPath != want.SocketPath ||
				target.PaneID != want.PaneID || len(spawner.args) != spawns {
				t.Fatalf("lost seat kill target=%+v err=%v spawns=%v; want %+v with %d spawns",
					target, err, spawner.args, want, spawns)
			}
			if row, found, err := database.Killed(context.Background(), "native-session"); err != nil || !found {
				t.Fatalf("kill record of native-session=%+v found=%t err=%v", row, found, err)
			}
			if row, found, err := database.Killed(context.Background(), lost.SeatID); err != nil || found {
				t.Fatalf("kill record of %s=%+v found=%t err=%v; want none", lost.SeatID, row, found, err)
			}
		})
	}
}
