package gather

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

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
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout(ctx))
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

// TmuxRead runs one read-only tmux command against the server at socketPath
// within ProbeTimeout, as ProbeServer does: a stopped or wedged server is this
// read's error, never a hang of its caller. A failure reads as every probe's
// does (tmuxReadFailure).
func TmuxRead(ctx context.Context, socketPath string, arguments ...string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout(ctx))
	defer cancel()
	output, err := pfmtmux.ExecWithin(probeCtx, probeTimeout(ctx), "", socketPath, arguments...).Output()
	if err != nil {
		return "", tmuxReadFailure(ctx, probeCtx, err, socketPath, arguments)
	}
	return string(output), nil
}

// tmuxReadFailure is a failed TmuxRead named by its command, in classifyProbe's
// words: a socket with no server behind it wraps ErrServerGone (its chat
// ended), one past the bound ErrServerUnresponsive; any other failure keeps
// tmux's reason.
func tmuxReadFailure(ctx, probeCtx context.Context, err error, socketPath string, arguments []string) error {
	if state, classified := classifyProbe(ctx, probeCtx, err, socketPath); state == ServerDead ||
		state == ServerUnresponsive {
		err = classified
	}
	return fmt.Errorf("tmux %s: %w", strings.Join(arguments, " "), withStderr(err))
}

// Unresponsive names a probe of socket that ran past its own deadline — the
// ProbeTimeout bound ctx gives it (probeTimeout) — while its caller's ctx was
// still live, nil for any other probe.
func Unresponsive(ctx, probeCtx context.Context, socket string) error {
	if ctx.Err() == nil && errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w within %s: %s", ErrServerUnresponsive, probeTimeout(ctx), socket)
	}
	return nil
}

// probeTimeoutKey is the context key of a probe bound other than
// ProbeTimeout. Nothing in production sets it; the package's own tests carry
// a short one on the context of a probe they point at a stopped server, so
// each call runs the same code without waiting out two seconds.
type probeTimeoutKey struct{}

// probeTimeout is the bound one tmux probe made under ctx runs within:
// ProbeTimeout, unless ctx carries a positive bound under probeTimeoutKey.
// Every probe deadline and every "did not answer within" message reads it.
func probeTimeout(ctx context.Context) time.Duration {
	if bound, carried := ctx.Value(probeTimeoutKey{}).(time.Duration); carried && bound > 0 {
		return bound
	}
	return ProbeTimeout
}

// classifyProbe reads one failed list-panes: past its own deadline the server
// is unresponsive, tmux's words name a dead or an empty (alive) server, and
// everything else is a probe that could not tell.
func classifyProbe(ctx, probeCtx context.Context, err error, socket string) (ServerState, error) {
	switch {
	case Unresponsive(ctx, probeCtx, socket) != nil:
		return ServerUnresponsive, Unresponsive(ctx, probeCtx, socket)
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
