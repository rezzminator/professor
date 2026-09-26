package tmux

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestSocketCommandJoinsDirAndSocket pins the shape kill/action/reap/spawn's
// own private command() methods used to carry as a copy each: Dir joined
// with a bare socket name, then Exec.
func TestSocketCommandJoinsDirAndSocket(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	output, err := Socket{Binary: fakeTmux(t, 0), Dir: dir}.
		Command(ctx, "cc-1-2-3", "list-panes", "-a").Output()
	if err != nil {
		t.Fatalf("Command().Output() = %v", err)
	}
	want := filepath.Join(dir, "cc-1-2-3")
	if !strings.Contains(string(output), want) {
		t.Fatalf("argv = %q, want it to address %q", output, want)
	}
}

// TestSocketCommandLeavesAFullPathUnchanged pins the OTHER shape a Dir-less
// Socket carries: kill's callers already hold a full socketPath, and
// filepath.Join("", full) must not corrupt it.
func TestSocketCommandLeavesAFullPathUnchanged(t *testing.T) {
	ctx := context.Background()
	full := filepath.Join(t.TempDir(), "cc-9-9-9")
	output, err := Socket{Binary: fakeTmux(t, 0)}.
		Command(ctx, full, "list-panes", "-a").Output()
	if err != nil {
		t.Fatalf("Command().Output() = %v", err)
	}
	if !strings.Contains(string(output), full) {
		t.Fatalf("argv = %q, want it to address %q unchanged", output, full)
	}
}

// TestSocketKillPaneAndKillServerAddressTheirTarget is the one KillPane/
// KillServer implementation action.TmuxExecutor and kill.TmuxKiller both
// used to carry a private copy of.
func TestSocketKillPaneAndKillServerAddressTheirTarget(t *testing.T) {
	ctx := context.Background()
	socket := Socket{Binary: fakeTmux(t, 0)}
	if err := socket.KillPane(ctx, "/sockets/cc-1", "%7"); err != nil {
		t.Fatalf("KillPane() = %v", err)
	}
	if err := socket.KillServer(ctx, "/sockets/cc-1"); err != nil {
		t.Fatalf("KillServer() = %v", err)
	}
}

// TestSocketKillPaneSurfacesAFailure: a real tmux failure (the target pane
// already gone) is never swallowed.
func TestSocketKillPaneSurfacesAFailure(t *testing.T) {
	ctx := context.Background()
	socket := Socket{Binary: fakeTmux(t, 1)}
	if err := socket.KillPane(ctx, "/sockets/cc-1", "%7"); err == nil {
		t.Fatal("KillPane() succeeded against a tmux that exited non-zero")
	}
}
