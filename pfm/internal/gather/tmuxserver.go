package gather

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
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
	probeCtx, cancel := ProbeContext(ctx)
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
	probeCtx, cancel := ProbeContext(ctx)
	defer cancel()
	output, err := pfmtmux.ExecWithin(probeCtx, ProbeTimeout, "", socketPath, arguments...).Output()
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
// ProbeTimeout bound ProbeContext gives it — while its caller's ctx was
// still live, nil for any other probe.
func Unresponsive(ctx, probeCtx context.Context, socket string) error {
	if ctx.Err() == nil && errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w within %s: %s", ErrServerUnresponsive, ProbeTimeout, socket)
	}
	return nil
}

// probeClockKey is the context key of the clock a probe bound is timed on
// (WithProbeClock). Nothing in production sets it.
type probeClockKey struct{}

// WithProbeClock is ctx whose probe bounds are timed on c: a test hands its
// fake clock here and moves it past the bound, so a probe of a server that
// never answers ends without the test waiting the bound out.
func WithProbeClock(ctx context.Context, c clock.Clock) context.Context {
	return context.WithValue(ctx, probeClockKey{}, c)
}

// ProbeContext is ctx bounded by ProbeTimeout: every probe
// deadline of a tmux server is made here. It is timed on the clock ctx
// carries (WithProbeClock), else on the real clock through context.WithTimeout
// itself — clock.WithTimeout's real path — so a bound that never fires is no
// armed real wait in every package whose tests reach a probe.
func ProbeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c, carried := ctx.Value(probeClockKey{}).(clock.Clock); carried && c != nil {
		return clock.WithTimeout(ctx, c, ProbeTimeout)
	}
	return context.WithTimeout(ctx, ProbeTimeout)
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
