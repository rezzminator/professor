package kill

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

// An UNIDENTIFIED live OpenCode seat answers to its socket name and nothing
// else — that name is where the chat is, never which conversation it is. A
// kill of such a seat must CLOSE the pane and record NOTHING: a tombstone
// keyed on a socket name names a chat nobody can ever unkill, survives the
// socket it was written for, and is ignored by the composer anyway
// (compose.applyKill). The pane closes; the ledger stays clean.
func TestKillOfASocketKeyedOpenCodeSeatClosesWithoutATombstone(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	spawner := &captureSpawner{}
	manager, err := New(database, Dependencies{
		Spawner: spawner,
		Now:     func() time.Time { return time.Unix(700, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}

	const socket = "ox-1700000000-42-7"
	target, err := manager.Kill(context.Background(), Request{
		ID:         socket,
		Engine:     pfmengine.OpenCode,
		Exit:       true,
		SocketName: socket,
		PaneID:     "%0",
	})
	if err != nil {
		t.Fatalf("Kill of a socket-keyed OpenCode seat: %v", err)
	}
	if len(spawner.args) != 1 {
		t.Fatalf("spawned = %#v, want the exit choreography for the live pane", spawner.args)
	}
	if got, want := spawner.args[0].SocketPath, filepath.Join(jail.tmuxDir, socket); got != want {
		t.Fatalf("socket path = %q, want %q", got, want)
	}
	if target.PaneID != "%0" {
		t.Fatalf("target = %#v, want the resolved pane", target)
	}
	killed, err := manager.Killed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 0 {
		t.Fatalf("killed rows = %#v, want none: a socket name is an address, not an identity", killed)
	}
}

// The identified twin is the control: once a session id IS pinned to the seat,
// the kill is an ordinary permanent kill and the tombstone is written.
func TestKillOfAnIdentifiedOpenCodeSeatStillRecordsItsTombstone(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	manager, err := New(database, Dependencies{
		Spawner: &captureSpawner{},
		Now:     func() time.Time { return time.Unix(700, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.Kill(context.Background(), Request{
		ID:         "ses_live",
		Engine:     pfmengine.OpenCode,
		Exit:       true,
		SocketName: "ox-1700000000-42-7",
		PaneID:     "%0",
	}); err != nil {
		t.Fatalf("Kill of an identified OpenCode seat: %v", err)
	}
	killed, err := manager.Killed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 1 || killed[0].ID != "ses_live" {
		t.Fatalf("killed rows = %#v, want the session's own tombstone", killed)
	}
}
