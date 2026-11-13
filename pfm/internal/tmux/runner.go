package tmux

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// runnerKey carries the deps.Runner a context's tmux calls run through.
type runnerKey struct{}

// WithClientRunner returns ctx carrying runner: every bounded tmux call made under
// it (Exec, ExecWithin, Socket, Observe) runs through runner.Run instead of
// its own exec, with the same argv, environment, directory and stdin, and is
// recorded the same way. A test scripts a deps.FakeRunner here (Script, or
// Handle for a server that hangs); production never sets one. ExecAttached
// is an interactive attach and always execs.
func WithClientRunner(ctx context.Context, runner deps.Runner) context.Context {
	return context.WithValue(ctx, runnerKey{}, runner)
}

func runnerFrom(ctx context.Context) deps.Runner {
	runner, _ := ctx.Value(runnerKey{}).(deps.Runner)
	return runner
}

// ErrRealServerInTest is the refusal a test process gets for a real tmux: a
// unit test fakes the server, a test of a real server asks for it.
var ErrRealServerInTest = errors.New(
	"a test process runs no real tmux: script a deps.FakeRunner through tmux.WithClientRunner(ctx, …), or call testjail.TmuxServer(t) for a real server",
)

// refuseInTest is the façade's answer to an exec of the real tmux in a test
// process that holds no testjail.TmuxServer allowance: an *exec.Error, so the
// caller reads it as a tmux that never started (CouldNotRun). The real tmux is
// a tmux (by name, or the registry's resolution of it) whose file, symlinks
// followed, lies outside the temporary directory: a script a test wrote there
// still runs, and so does another launcher.
func refuseInTest(argv0 string) error {
	if !testing.Testing() || filepath.Base(argv0) != Binary && argv0 != deps.Executable(Binary) ||
		deps.AllowedInTest(Binary) || !realBinary(argv0) {
		return nil
	}
	return &exec.Error{Name: argv0, Err: ErrRealServerInTest}
}

// realBinary reports whether argv0 resolves to a file outside the temporary
// directories a test writes under (TMPDIR and the system's, where the short
// jail roots live); one exec cannot find is no real binary (exec reports it).
func realBinary(argv0 string) bool {
	path, err := exec.LookPath(argv0)
	if err != nil {
		return false
	}
	path = symlinkTarget(path)
	for _, temp := range []string{paths.TempRoot(), paths.SystemTempDir()} {
		inside, err := filepath.Rel(symlinkTarget(temp), path)
		if err == nil && inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// symlinkTarget is path with its symlinks followed, or path as it is.
func symlinkTarget(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// refused is refuseInTest for a command that execs (no runner).
func (command *Cmd) refused() error {
	if command.runner != nil {
		return nil
	}
	return refuseInTest(command.Args[0])
}

// wait is Wait's completion: the runner-run Start's outcome, or exec's.
func (command *Cmd) wait() error {
	if command.waited != nil {
		return <-command.waited
	}
	return command.Cmd.Wait()
}

// ExitError is a tmux run through a runner that exited non-zero: the code and
// the stderr it printed, read as exec.ExitError's ("exit status N").
type ExitError struct {
	Code   int
	Stderr []byte
}

func (err *ExitError) Error() string { return "exit status " + strconv.Itoa(err.Code) }

// ExitCode is the tmux client's exit code, as exec.ExitError.ExitCode.
func (err *ExitError) ExitCode() int { return err.Code }

// ExitStderr is what a tmux client that exited non-zero wrote to stderr, the
// client exec'd (*exec.ExitError) or run through a runner (*ExitError); ok is
// false for any other error.
func ExitStderr(err error) (stderr []byte, ok bool) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Stderr, true
	}
	var runnerExit *ExitError
	if errors.As(err, &runnerExit) {
		return runnerExit.Stderr, true
	}
	return nil, false
}

// runThrough runs the command on its runner as exec would run it: a context
// already done never starts it, stdin is read whole, the output reaches the
// command's own Stdout and Stderr writers when set, and a non-zero exit is
// an *ExitError carrying the stderr.
func (command *Cmd) runThrough() (deps.RunResult, error) {
	if err := command.ctx.Err(); err != nil {
		return deps.RunResult{ExitCode: -1}, err
	}
	var stdin []byte
	if command.Stdin != nil {
		read, err := io.ReadAll(command.Stdin)
		if err != nil {
			return deps.RunResult{ExitCode: -1}, err
		}
		stdin = read
	}
	result, err := command.runner.Run(command.ctx, append([]string(nil), command.Args...), deps.RunOptions{
		Env: command.Env, Dir: command.Dir, Stdin: stdin, WaitDelay: command.WaitDelay,
	})
	if err != nil {
		return result, err
	}
	for _, out := range []struct {
		writer io.Writer
		bytes  []byte
	}{{command.Stdout, result.Stdout}, {command.Stderr, result.Stderr}} {
		if out.writer != nil {
			if _, err := out.writer.Write(out.bytes); err != nil {
				return result, err
			}
		}
	}
	if result.ExitCode != 0 {
		return result, &ExitError{Code: result.ExitCode, Stderr: result.Stderr}
	}
	return result, nil
}

// outputThrough is Output and CombinedOutput on the runner: exec's own refusal
// of a writer the caller already set, then the captured stream(s).
func (command *Cmd) outputThrough(combined bool) ([]byte, error) {
	if command.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if combined && command.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	result, err := command.runThrough()
	if combined {
		return append(append([]byte(nil), result.Stdout...), result.Stderr...), err
	}
	return result.Stdout, err
}

// startThrough is Start on the runner: the binary is looked up as exec's Start
// looks it up, then the run completes in the background for Wait.
func (command *Cmd) startThrough() error {
	if err := command.ctx.Err(); err != nil {
		return err
	}
	if _, err := command.runner.LookPath(command.Args[0]); err != nil {
		return err
	}
	command.waited = make(chan error, 1)
	go func() {
		_, err := command.runThrough()
		command.waited <- err
	}()
	return nil
}
