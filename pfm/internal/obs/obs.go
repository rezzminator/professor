// Package obs is pfm's activity log: one structured destination for every
// corner of the engine, per pfm home
// (docs/dev/trains/testing-foundation/waves/6-activity-log/spec.md).
//
// OpenLog installs the process logger once, at process entry, over a
// JSON-lines file under the resolved home — so a fence, a test jail, a lane
// container and the live host each write their own file and can never mix.
// Logger reads the logger a context carries, With scopes one into a context,
// and Span times a unit of work. Every record is scrubbed by Scrub before it
// reaches the file: a key nobody declared, or a value that looks like a
// credential or a body, never lands on disk.
package obs

import (
	"context"
	"log/slog"

	"hostops/pfm/internal/clock"
)

// Field names every record carries, and the scoped ones a caller adds. They
// are named here so a call site spells a field the same way the reader
// (`pfm log --chat`) filters it.
const (
	FieldCmd     = "cmd"
	FieldPID     = "pid"
	FieldVersion = "version"
	FieldChat    = "chat"
	FieldSeat    = "seat"
	FieldEngine  = "engine"
	FieldSock    = "sock"
	FieldDur     = "dur_ms"
	FieldErr     = "err"
	FieldExit    = "exit"
	// FieldTime is the timestamp key: slog's "time" renamed, because every
	// other pfm surface spells it ts.
	FieldTime = "ts"
)

// contextKey is this package's private context key type.
type contextKey struct{}

// scope is what a context carries: the logger records are written through and
// the clock Span measures with.
type scope struct {
	logger *slog.Logger
	timing clock.Clock
}

// Logger returns the logger ctx carries, or the process logger OpenLog
// installed. Before OpenLog — a unit test that never opened one — it is a
// logger that discards, never nil.
func Logger(ctx context.Context) *slog.Logger {
	return current(ctx).logger
}

// With returns a context whose logger carries kv in addition to what it
// already carries: obs.With(ctx, obs.FieldChat, id, obs.FieldSeat, seat).
func With(ctx context.Context, kv ...any) context.Context {
	if len(kv) == 0 {
		return ctx
	}
	existing := current(ctx)
	return context.WithValue(ctx, contextKey{}, &scope{logger: existing.logger.With(kv...), timing: existing.timing})
}

// Span logs the entry of a unit of work and returns the function that logs
// its exit: duration in FieldDur, and FieldErr plus error level when end is
// handed a non-nil error. The returned function must be called exactly once —
// a span that never ends leaves no exit record, which reads as a unit of work
// still running.
func Span(ctx context.Context, name string) func(err error) {
	existing := current(ctx)
	started := existing.timing.Now()
	existing.logger.LogAttrs(ctx, slog.LevelDebug, name+".start")
	return func(err error) {
		elapsed := existing.timing.Now().Sub(started).Milliseconds()
		level := slog.LevelDebug
		attrs := []slog.Attr{slog.Int64(FieldDur, elapsed)}
		if err != nil {
			level = slog.LevelError
			attrs = append(attrs, slog.String(FieldErr, err.Error()))
		}
		existing.logger.LogAttrs(ctx, level, name+".end", attrs...)
	}
}

// Clock returns the clock in play for ctx: the scoped one a context carries,
// else the process clock (clock.Real, unless OpenLog was opened with a
// different one). A wrapper outside this package that times its own door —
// the tmux door among them — reads the door's completion time through this,
// the same clock every middleware in this package reads through current, so
// a test's fake clock reaches every door uniformly rather than one wrapper
// quietly keeping the wall clock.
func Clock(ctx context.Context) clock.Clock {
	return current(ctx).timing
}

// current resolves the scope in play: the context's, else the process one.
func current(ctx context.Context) *scope {
	if ctx != nil {
		if carried, ok := ctx.Value(contextKey{}).(*scope); ok && carried != nil {
			return carried
		}
	}
	return processScope()
}
