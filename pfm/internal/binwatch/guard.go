package binwatch

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"hostops/pfm/internal/clock"
)

// Guard is Serve's door for the long-lived pfm server that has no listener to
// stop: the stdio MCP server, which one chat launches and nobody ever closes.
// It outlives every install in its chat's session, so it kept answering
// chat_kill and chat_status out of a fleet composer days older than the build
// the caller believed it was talking to — a stale answer wearing a current
// version string, which is worse than no answer.
//
// It returns a context that ends when this process's executable is replaced, a
// predicate naming whether that replacement is why the served call returned,
// and a stop to release the watcher. The caller exits ExitReplaced so its
// client starts the next server on the new build.
func Guard(parent context.Context, stderr io.Writer) (context.Context, func() bool, func()) {
	watchCtx, stopWatch := context.WithCancel(parent)
	ctx, replaced, stop := guardReplacement(parent, ownReplacement(watchCtx, stderr, clock.Real), stderr)
	return ctx, replaced, func() {
		stop()
		stopWatch()
	}
}

// guardReplacement is Guard with the replacement signal injected — the same
// split Serve and serve use, so the wiring is testable without replacing a
// real binary.
func guardReplacement(
	parent context.Context,
	replaced <-chan struct{},
	stderr io.Writer,
) (context.Context, func() bool, func()) {
	ctx, cancel := context.WithCancel(parent)
	var seen atomic.Bool
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case <-replaced:
		}
		// The reason is recorded BEFORE the cancel: the caller reads the
		// predicate the moment its served call returns, and a cancel that
		// arrives first would be read as the server's own ending.
		seen.Store(true)
		fmt.Fprintln(
			stderr,
			"pfm mcp: own executable was replaced by a new build; ending this server so the next call starts on it",
		)
		cancel()
	}()
	return ctx, seen.Load, func() {
		close(done)
		cancel()
	}
}
