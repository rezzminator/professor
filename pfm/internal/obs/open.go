package obs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/paths"
)

// AlphaSuffix marks a pre-release build. A build carrying it logs at debug by
// default — an alpha tester's bug report is only as good as what pfm wrote
// while it happened — and a release build logs at info.
const AlphaSuffix = "-alpha"

// StderrMirror is the value paths.EnvLogMirror takes to mirror every record
// onto stderr for a foreground run.
const StderrMirror = "stderr"

// Settings is what a process entry tells the activity log: which verb is
// running, which build, and the machine's log policy from pfm.config.json
// (Level and Components as § Control spells them; KeepDays 0 disables the
// time limit). Everything else has a default: Path falls back to the resolved
// home's pfm.jsonl, Env to the real process, Timing to the wall clock, Stderr
// to os.Stderr.
type Settings struct {
	Cmd        string
	Version    string
	Level      string
	Components map[string]string
	KeepFiles  int
	MaxMB      int
	KeepDays   int
	Path       string
	Env        paths.Env
	Timing     clock.Clock
	Stderr     io.Writer
}

// process holds the logger OpenLog installed. Until then every Logger call
// answers with a discarding logger — never nil, so a package that logs before
// the process opened one (a unit test, a library caller) is silent rather
// than broken.
var (
	processMutex sync.Mutex
	process      = &scope{logger: slog.New(discardHandler{}), timing: clock.Real}
)

// processScope returns the installed process scope.
func processScope() *scope {
	processMutex.Lock()
	defer processMutex.Unlock()
	return process
}

// OpenLog installs the process logger and writes the cmd.start record. The
// returned context carries the same logger for every span below it; the
// returned function writes cmd.exit with the process's exit code and total
// duration, then closes the file — call it exactly once, at process exit.
//
// A destination that cannot be resolved or opened is NOT silent: the reason
// goes to Settings.Stderr and the process keeps running against a discarding
// logger, because losing the activity log is never a reason to fail a verb.
// A global level of `off` is the one quiet case: nothing is recorded and no
// file is created.
func OpenLog(ctx context.Context, settings Settings) (context.Context, func(exitCode int)) {
	env := settings.Env
	if env == nil {
		env = paths.OSEnv{}
	}
	timing := settings.Timing
	if timing == nil {
		timing = clock.Real
	}
	stderr := settings.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	levels, levelErr := ResolveLevels(
		Policy{Version: settings.Version, Level: settings.Level, Components: settings.Components}, env,
	)
	if levelErr != nil {
		fmt.Fprintf(stderr, "pfm: activity log: %v\n", levelErr)
	}
	destination, closer := slog.Handler(discardHandler{}), io.Closer(nil)
	if levels.Global.Level != LevelOff {
		var openErr error
		destination, closer, openErr = openDestination(env, settings, timing, stderr)
		if openErr != nil {
			fmt.Fprintf(stderr, "pfm: activity log unavailable: %v\n", openErr)
		}
		destination = levelHandler{next: destination, levels: levels}
	}
	logger := slog.New(destination).With(
		FieldCmd, settings.Cmd,
		FieldPID, os.Getpid(),
		FieldVersion, settings.Version,
	)
	opened := &scope{logger: logger, timing: timing}
	processMutex.Lock()
	process = opened
	processMutex.Unlock()
	started := timing.Now()
	logger.LogAttrs(ctx, slog.LevelInfo, "cmd.start")
	scoped := context.WithValue(ctx, contextKey{}, opened)
	return scoped, func(exitCode int) {
		logger.LogAttrs(
			scoped,
			slog.LevelInfo,
			"cmd.exit",
			slog.Int(FieldExit, exitCode),
			slog.Int64(FieldDur, timing.Now().Sub(started).Milliseconds()),
		)
		if closer != nil {
			if err := closer.Close(); err != nil {
				fmt.Fprintf(stderr, "pfm: activity log: %v\n", err)
			}
		}
	}
}

// openDestination builds the handler records are written through: the home's
// pfm.jsonl, plus a stderr mirror when paths.EnvLogMirror asks for one. The
// destination takes every level — levelHandler in front of it owns the
// control — and applies Scrub last. A failure returns the discarding handler
// AND the reason — the caller reports it, so a log nobody can write never
// reads as a quiet one.
func openDestination(
	env paths.Env,
	settings Settings,
	timing clock.Clock,
	stderr io.Writer,
) (slog.Handler, io.Closer, error) {
	options := &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: Scrub}
	mirror := env.Get(paths.EnvLogMirror) == StderrMirror
	path := settings.Path
	if path == "" {
		resolved, err := paths.Resolve()
		if err != nil {
			return mirrorOnly(mirror, stderr, options), nil, fmt.Errorf("resolve log path: %w", err)
		}
		path = resolved.LogFile
	}
	writer, err := newRotator(path, settings.KeepFiles, settings.MaxMB, settings.KeepDays, timing)
	if err != nil {
		return mirrorOnly(mirror, stderr, options), nil, err
	}
	handler := slog.Handler(slog.NewJSONHandler(writer, options))
	if mirror {
		handler = fanout{handler, slog.NewJSONHandler(stderr, options)}
	}
	return handler, writer, nil
}

// mirrorOnly is the destination left when the file could not be opened: the
// stderr mirror if one was asked for, and otherwise nothing at all.
func mirrorOnly(mirror bool, stderr io.Writer, options *slog.HandlerOptions) slog.Handler {
	if mirror {
		return slog.NewJSONHandler(stderr, options)
	}
	return discardHandler{}
}

// Verb names the command a process is running, for the cmd field: the first
// argument, or "picker" for the bare `pfm` that opens the chat picker.
func Verb(args []string) string {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "picker"
	}
	return args[0]
}

// fanout writes every record to each handler — the stderr mirror.
type fanout []slog.Handler

func (handlers fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (handlers fanout) Handle(ctx context.Context, record slog.Record) error {
	var failure error
	for _, handler := range handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil && failure == nil {
			failure = fmt.Errorf("write activity record: %w", err)
		}
	}
	return failure
}

func (handlers fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make(fanout, 0, len(handlers))
	for _, handler := range handlers {
		next = append(next, handler.WithAttrs(attrs))
	}
	return next
}

func (handlers fanout) WithGroup(name string) slog.Handler {
	next := make(fanout, 0, len(handlers))
	for _, handler := range handlers {
		next = append(next, handler.WithGroup(name))
	}
	return next
}

// discardHandler drops every record: the destination before OpenLog, and the
// one left when the file could not be opened.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }
