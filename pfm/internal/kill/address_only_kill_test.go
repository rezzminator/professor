package kill

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// A live seat that resolved by name carries its tmux address but no session id
// (a pane whose Claude never wrote a session, a pane named by window name
// only). Killing it must close the pane and record nothing: there is no
// identity to tombstone.
func TestKillOfAnIdlessLiveAddressClosesItsPaneWithoutATombstone(t *testing.T) {
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

	const socket = "cc-1700000000-42-7"
	target, err := manager.Kill(context.Background(), Request{
		Engine:     pfmengine.Claude,
		SocketName: socket,
		PaneID:     "%0",
	})
	if err != nil {
		t.Fatalf("Kill of an id-less live address: %v", err)
	}
	if len(spawner.args) != 1 {
		t.Fatalf("spawned = %#v, want the exit choreography for the live pane", spawner.args)
	}
	if got, want := spawner.args[0].SocketPath, filepath.Join(jail.tmuxDir, socket); got != want {
		t.Fatalf("socket path = %q, want %q", got, want)
	}
	if target.ID != socket || target.PaneID != "%0" {
		t.Fatalf("target = %#v, want the socket-keyed address and the resolved pane", target)
	}
	killed, err := manager.Killed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 0 {
		t.Fatalf("killed rows = %#v, want none: an address names where a chat is, not which", killed)
	}
}

// A Booting Claude row carries its socket name as its id. That is the same
// address-only shape: closing the pane is right, a tombstone nobody can unkill
// is not.
func TestKillOfASocketKeyedClaudeSeatClosesWithoutATombstone(t *testing.T) {
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

	const socket = "cc-1700000000-42-7"
	if _, err := manager.Kill(context.Background(), Request{
		ID:         socket,
		Engine:     pfmengine.Claude,
		SocketName: socket,
		PaneID:     "%0",
	}); err != nil {
		t.Fatalf("Kill of a socket-keyed Claude seat: %v", err)
	}
	if len(spawner.args) != 1 {
		t.Fatalf("spawned = %#v, want the exit choreography for the live pane", spawner.args)
	}
	killed, err := manager.Killed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 0 {
		t.Fatalf("killed rows = %#v, want none: a socket name is an address, not an identity", killed)
	}
}

func TestKillWithNeitherIdNorAddressStillRefuses(t *testing.T) {
	jail := newKillJail(t)
	manager, err := New(jail.open(t), Dependencies{Spawner: &captureSpawner{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Kill(context.Background(), Request{Engine: pfmengine.Claude}); err == nil {
		t.Fatal("Kill with no id and no address succeeded, want a refusal")
	}
}

func TestAddressOnly(t *testing.T) {
	for name, test := range map[string]struct {
		target Target
		want   bool
	}{
		"no id":              {Target{Engine: pfmengine.Claude}, true},
		"id is socket":       {Target{ID: "cc-1-2-3", SocketName: "cc-1-2-3"}, true},
		"id with live seat":  {Target{ID: "ses_live", SocketName: "cc-1-2-3"}, false},
		"id without address": {Target{ID: "ses_live"}, false},
	} {
		if got := AddressOnly(test.target); got != test.want {
			t.Errorf("%s: AddressOnly(%#v) = %v, want %v", name, test.target, got, test.want)
		}
	}
}
