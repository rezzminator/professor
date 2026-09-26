package tmux

import (
	"context"
	"path/filepath"
)

// Socket is the one "address a tmux socket, then Exec" wrapper: Dir joined
// with a bare socket name (action/reap/spawn's shape), or Dir left empty
// when the caller already holds a full socket path (kill's shape — its
// callers pass Target.SocketPath directly, and filepath.Join("", full)
// returns full unchanged). Four consumer packages — kill, action, reap,
// spawn — each used to carry this join, plus their own copy of KillPane and
// KillServer, as a private method; pfm/CLAUDE.md K3 wants exactly one.
type Socket struct {
	Binary string
	Dir    string
}

// Command builds one observed tmux invocation at socket.
func (s Socket) Command(ctx context.Context, socket string, arguments ...string) *Cmd {
	return Exec(ctx, s.Binary, filepath.Join(s.Dir, socket), arguments...)
}

// KillPane runs tmux kill-pane against one pane.
func (s Socket) KillPane(ctx context.Context, socket, paneID string) error {
	return s.Command(ctx, socket, "kill-pane", "-t", paneID).Run()
}

// KillServer runs tmux kill-server against one whole socket.
func (s Socket) KillServer(ctx context.Context, socket string) error {
	return s.Command(ctx, socket, "kill-server").Run()
}
