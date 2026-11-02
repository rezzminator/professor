package gather

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// ServerState is the one answer to "is the tmux server behind this socket
// running": every liveness question about a tmux chat server asks ProbeServer.
type ServerState int

const (
	// ServerUnknown is the state of a probe that failed: tmux could not run,
	// or the server answered with something other than its panes or its
	// absence. It is never read as dead.
	ServerUnknown ServerState = iota
	// ServerAlive is a server that answered, with or without panes.
	ServerAlive
	// ServerDead is tmux's own word that nothing listens on the socket.
	ServerDead
	// ServerUnresponsive is a server that did not answer within ProbeTimeout:
	// stopped or wedged, it may still hold a live chat, so it is never dead.
	ServerUnresponsive
)

func (state ServerState) String() string {
	switch state {
	case ServerAlive:
		return "alive"
	case ServerDead:
		return "dead"
	case ServerUnresponsive:
		return "unresponsive"
	default:
		return "unknown"
	}
}

// ProbeServer is the one bounded read of a tmux server: it asks the server at
// socketPath (tmux -S) for its panes in the scan's own format (paneFormat),
// bounded by ProbeTimeout. A dead server answers ServerDead with a nil error;
// an empty server is alive with no panes; a server past the bound answers
// ServerUnresponsive and an error wrapping ErrServerUnresponsive — never "not
// here"; any other failure, an unreadable answer included, is ServerUnknown
// and its error. binary "" is the registered tmux.
func ProbeServer(ctx context.Context, binary, socketPath string) (ServerState, []ProbePane, error) {
	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	output, err := pfmtmux.Exec(probeCtx, binary, socketPath, "list-panes", "-a", "-F", paneFormat).Output()
	if err != nil {
		state, classified := classifyProbe(ctx, probeCtx, err, socketPath)
		switch state {
		case ServerDead:
			return ServerDead, nil, nil
		case ServerAlive:
			return ServerAlive, nil, nil
		}
		return state, nil, classified
	}
	panes, err := parseTmuxPanes(filepath.Base(socketPath), output)
	if err != nil {
		return ServerUnknown, nil, err
	}
	return ServerAlive, panes, nil
}

// ProbePanes is ProbeServer for a caller that needs the panes: only a server
// that answered lists any; a dead one is ErrServerGone, an unresponsive one
// ErrServerUnresponsive — neither is ever an empty list.
func ProbePanes(ctx context.Context, binary, socketPath string) ([]ProbePane, error) {
	state, panes, err := ProbeServer(ctx, binary, socketPath)
	switch state {
	case ServerAlive:
		return panes, nil
	case ServerDead:
		return nil, fmt.Errorf("%w: %s", ErrServerGone, socketPath)
	}
	return nil, err
}

// classifyProbe reads one failed list-panes: past its own deadline the server
// is unresponsive, tmux's words name a dead or an empty (alive) server, and
// everything else is a probe that could not tell.
func classifyProbe(ctx, probeCtx context.Context, err error, socket string) (ServerState, error) {
	switch {
	case ctx.Err() == nil && errors.Is(probeCtx.Err(), context.DeadlineExceeded):
		return ServerUnresponsive, fmt.Errorf("%w within %s: %s", ErrServerUnresponsive, ProbeTimeout, socket)
	case pfmtmux.CouldNotRun(err):
		return ServerUnknown, fmt.Errorf("could not run tmux to probe %s: %w", socket, err)
	case TmuxServerGone(err):
		return ServerDead, fmt.Errorf("%w: %s", ErrServerGone, socket)
	case serverEmpty(err):
		return ServerAlive, fmt.Errorf("%w: %s", ErrServerEmpty, socket)
	default:
		return ServerUnknown, fmt.Errorf("probe tmux server %s: %w", socket, withStderr(err))
	}
}
