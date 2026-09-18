package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"time"

	"hostops/pfm/internal/deps"
)

// compRunner is the component every process door records under.
const compRunner = "runner"

// Runner decorates next with the runner component's records (spec
// § Middleware, `runner`): one record per Run, LookPath and Start, and one
// per terminal of a started Process — Wait, Release, Kill, KillGroup. It
// never changes what next does: the argv, options, result and error pass
// through untouched. What is recorded is the argument SHAPE — argv[0]'s
// base name and argc — never an argument, so a token on a command line
// cannot reach the file. nil next wraps deps.RealRunner.
func Runner(next deps.Runner) deps.Runner {
	if next == nil {
		next = deps.RealRunner{}
	}
	return loggedRunner{next: next}
}

type loggedRunner struct {
	next deps.Runner
}

// Run records runner.run: INFO with the exit code when the command ran
// (a non-zero exit is the command answering, per deps.Runner's contract),
// ERROR with exit -1 when it never started.
func (runner loggedRunner) Run(ctx context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error) {
	started := current(ctx).timing.Now()
	result, err := runner.next.Run(ctx, argv, opts)
	exit := result.ExitCode
	if err != nil {
		exit = -1
	}
	attrs := append(argvShape(argv), slog.Int(FieldExit, exit))
	record(ctx, compRunner, "runner.run", errorLevel(err, slog.LevelInfo), started, err, attrs...)
	return result, err
}

// LookPath records runner.lookpath: INFO with the resolved path, WARN with
// err on a miss — an absent optional binary is an answer, not a failure of
// the door, and the caller decides what it means.
func (runner loggedRunner) LookPath(name string) (string, error) {
	ctx := context.Background()
	started := current(ctx).timing.Now()
	path, err := runner.next.LookPath(name)
	record(ctx, compRunner, "runner.lookpath", errorLevel(err, slog.LevelInfo, slog.LevelWarn), started, err,
		slog.String("argv", name), slog.String("path", path))
	return path, err
}

// Start records runner.start with the pid, then hands back a Process whose
// terminals record their own result; a Start that fails is one ERROR record.
func (runner loggedRunner) Start(ctx context.Context, argv []string, opts deps.StartOptions) (deps.Process, error) {
	started := current(ctx).timing.Now()
	process, err := runner.next.Start(ctx, argv, opts)
	attrs := argvShape(argv)
	if err != nil {
		record(ctx, compRunner, "runner.start", slog.LevelError, started, err, attrs...)
		return nil, err
	}
	attrs = append(attrs, slog.Int(FieldPID, process.Pid()))
	record(ctx, compRunner, "runner.start", slog.LevelInfo, started, nil, attrs...)
	return &loggedProcess{ctx: ctx, next: process, started: started}, nil
}

// loggedProcess is the started child seen through the runner component:
// every terminal writes one record carrying the pid and dur_ms since start.
type loggedProcess struct {
	ctx     context.Context
	next    deps.Process
	started time.Time
}

func (process *loggedProcess) Pid() int { return process.next.Pid() }

// Wait records runner.exit: the child's exit code at INFO when it exited
// non-zero (an *exec.ExitError is the child answering), ERROR with exit -1
// for any other failure to wait.
func (process *loggedProcess) Wait() error {
	err := process.next.Wait()
	exit, level := 0, slog.LevelInfo
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	default:
		exit, level = -1, slog.LevelError
	}
	process.terminal("runner.exit", level, err, slog.Int(FieldExit, exit))
	return err
}

func (process *loggedProcess) Release() error {
	err := process.next.Release()
	process.terminal("runner.release", errorLevel(err, slog.LevelInfo), err)
	return err
}

// Kill and KillGroup forward to the inner Process when it has them (the
// sibling's Runner grows both; this worktree's deps.Process does not carry
// them yet) and record the terminal either way — a kill nobody could
// forward is an ERROR naming the pid, never a silent no-op.
func (process *loggedProcess) Kill() error {
	killer, ok := process.next.(interface{ Kill() error })
	return process.forwardKill("runner.kill", ok, func() error { return killer.Kill() })
}

func (process *loggedProcess) KillGroup() error {
	killer, ok := process.next.(interface{ KillGroup() error })
	return process.forwardKill("runner.killgroup", ok, func() error { return killer.KillGroup() })
}

func (process *loggedProcess) forwardKill(op string, forwardable bool, kill func() error) error {
	var err error
	if forwardable {
		err = kill()
	} else {
		err = fmt.Errorf("obs: %s: process %d cannot be killed through this Runner", op, process.Pid())
	}
	process.terminal(op, errorLevel(err, slog.LevelInfo), err)
	return err
}

func (process *loggedProcess) terminal(op string, level slog.Level, err error, attrs ...slog.Attr) {
	record(process.ctx, compRunner, op, level, process.started, err,
		append([]slog.Attr{slog.Int(FieldPID, process.Pid())}, attrs...)...)
}

// argvShape is the only thing a process door tells the log about its command
// line: the binary's base name and how many words followed it.
func argvShape(argv []string) []slog.Attr {
	name := ""
	if len(argv) != 0 {
		name = filepath.Base(argv[0])
	}
	return []slog.Attr{slog.String("argv", name), slog.Int("argc", len(argv))}
}

// errorLevel picks the level a record takes: ok when err is nil, else the
// failure level (ERROR unless the caller names a softer one).
func errorLevel(err error, ok slog.Level, failure ...slog.Level) slog.Level {
	if err == nil {
		return ok
	}
	if len(failure) != 0 {
		return failure[0]
	}
	return slog.LevelError
}

// record writes one middleware record for comp: op as the message, dur_ms
// since started on the scope's clock, err when there is one, plus attrs. It
// is the one shape every door in this package writes.
func record(ctx context.Context, comp, op string, level slog.Level, started time.Time, err error, attrs ...slog.Attr) {
	scoped := Component(ctx, comp)
	existing := current(scoped)
	attrs = append(attrs, slog.Int64(FieldDur, existing.timing.Now().Sub(started).Milliseconds()))
	if err != nil {
		attrs = append(attrs, slog.String(FieldErr, err.Error()))
	}
	existing.logger.LogAttrs(scoped, level, op, attrs...)
}

// Started is the runner door for a process that has not migrated behind
// deps.Runner yet (spec § Middleware names four): call it right after the
// *exec.Cmd started, with its argv and pid, and call the returned function
// exactly once with Wait's error. It writes the same runner.start and
// runner.exit records a wrapped Runner writes, so the census reads one shape.
func Started(ctx context.Context, argv []string, pid int) func(waitErr error) {
	started := current(ctx).timing.Now()
	record(ctx, compRunner, "runner.start", slog.LevelInfo, started, nil, append(argvShape(argv), slog.Int(FieldPID, pid))...)
	process := &loggedProcess{ctx: ctx, next: waitedProcess{pid: pid}, started: started}
	return func(waitErr error) {
		process.next = waitedProcess{pid: pid, err: waitErr}
		_ = process.Wait()
	}
}

// waitedProcess is the Process shape Started's terminal reuses so the exit
// classification (exit code vs failure) lives in one place: loggedProcess.Wait.
type waitedProcess struct {
	pid int
	err error
}

func (process waitedProcess) Pid() int       { return process.pid }
func (process waitedProcess) Wait() error    { return process.err }
func (process waitedProcess) Release() error { return nil }

// StartFailed is Started's counterpart for a direct door whose *exec.Cmd
// never started: the one ERROR record a wrapped Runner.Start would write.
func StartFailed(ctx context.Context, argv []string, err error) {
	record(ctx, compRunner, "runner.start", slog.LevelError, current(ctx).timing.Now(), err, argvShape(argv)...)
}
