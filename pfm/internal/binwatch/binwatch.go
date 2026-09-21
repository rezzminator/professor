// Package binwatch ends a long-lived pfm server when the binary it runs is
// replaced, so its service manager restarts it on the new build.
//
// Replacing ~/.local/bin/pfm never touches a process already running the old
// image, and the shared MCP daemon is the one pfm process nobody closes: it
// kept serving a days-old build through installs that each believed they had
// shipped a fix. Serve watches the executable and exits with ExitReplaced the
// moment it changes — systemd's Restart=on-failure and launchd's KeepAlive
// both act on that — so every install path refreshes the daemon, not only the
// ones that remember to restart it.
package binwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// ExitReplaced is the server's exit status when its binary was replaced under
// it. Non-zero on purpose: systemd's Restart=on-failure acts on it, and
// launchd's KeepAlive restarts on any exit. 75 is EX_TEMPFAIL — "try again",
// which is exactly what the supervisor does.
const ExitReplaced = 75

// watchInterval is how often the executable's identity is re-read;
// shutdownGrace bounds how long in-flight requests get to finish once a
// replacement is seen.
const (
	watchInterval = 5 * time.Second
	shutdownGrace = 30 * time.Second
)

// Serve serves until the listener fails or this process's executable is
// replaced. A server that cannot watch itself still serves, but says — on
// every start — that an install will NOT refresh it.
func Serve(server *http.Server, listener net.Listener, stderr io.Writer) int {
	return ServeWithClock(server, listener, stderr, clock.Real)
}

// ServeWithClock is Serve with the polling clock injected for a jailed daemon
// or a deterministic unit test.
func ServeWithClock(server *http.Server, listener net.Listener, stderr io.Writer, clk clock.Clock) int {
	if clk == nil {
		clk = clock.Real
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	return serve(server, listener, ownReplacement(ctx, stderr, clk), stderr)
}

func ownReplacement(ctx context.Context, stderr io.Writer, clk clock.Clock) <-chan struct{} {
	path, err := os.Executable()
	if err == nil {
		var replaced <-chan struct{}
		if replaced, err = watchWithClock(ctx, path, watchInterval, stderr, clk); err == nil {
			return replaced
		}
	}
	fmt.Fprintf(
		stderr,
		"pfm mcp serve: cannot watch own executable (%v); a new install will NOT restart this daemon — restart it by hand after every install\n",
		err,
	)
	// A nil channel never fires: the server serves the build it started on.
	return nil
}

// watch reports, by closing the returned channel, the moment path no longer
// names the file this process started from. A rename over the path (make
// host-install's atomic mv) changes the inode; a copy over it changes size or
// mtime — either one is a replacement.
//
// A path that cannot be read mid-run is said out loud once per outage and never
// treated as a replacement: restarting onto a binary that is not there would
// take the server down with nothing to come back on.
func watch(ctx context.Context, path string, interval time.Duration, stderr io.Writer) (<-chan struct{}, error) {
	return watchWithClock(ctx, path, interval, stderr, clock.Real)
}

func watchWithClock(
	ctx context.Context,
	path string,
	interval time.Duration,
	stderr io.Writer,
	clk clock.Clock,
) (<-chan struct{}, error) {
	if clk == nil {
		clk = clock.Real
	}
	started, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat own executable %s: %w", path, err)
	}
	replaced := make(chan struct{})
	go func() {
		ticker := clk.NewTicker(interval)
		defer ticker.Stop()
		unreadable := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
			}
			current, err := os.Stat(path)
			if err != nil {
				if !unreadable {
					fmt.Fprintf(
						stderr,
						"pfm mcp serve: cannot stat own executable %s (%v); still serving the build it started on\n",
						path,
						err,
					)
				}
				unreadable = true
				continue
			}
			unreadable = false
			if !os.SameFile(started, current) || current.Size() != started.Size() ||
				!current.ModTime().Equal(started.ModTime()) {
				close(replaced)
				return
			}
		}
	}()
	return replaced, nil
}

// serve is Serve with the replacement signal injected. On a replacement it
// stops accepting, lets in-flight requests finish within shutdownGrace, and
// returns ExitReplaced for the supervisor to act on.
func serve(server *http.Server, listener net.Listener, replaced <-chan struct{}, stderr io.Writer) int {
	shutdownDone := make(chan struct{})
	stopWatching := make(chan struct{})
	restarting := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-stopWatching:
			return
		case <-replaced:
		}
		close(restarting)
		fmt.Fprintln(
			stderr,
			"pfm mcp serve: own executable was replaced by a new build; exiting so the service manager restarts the daemon on it",
		)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			fmt.Fprintf(stderr, "pfm mcp serve: shutdown for restart: %v\n", err)
		}
	}()
	err := server.Serve(listener)
	select {
	case <-restarting:
		<-shutdownDone
		return ExitReplaced
	default:
	}
	close(stopWatching)
	<-shutdownDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "pfm mcp serve: %v\n", err)
		return 1
	}
	return 0
}
