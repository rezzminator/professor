package tmux

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// comp is the activity-log component every tmux invocation records under.
const comp = "tmux"

// Cmd is one tmux invocation that owns its completion: Command's *exec.Cmd,
// embedded, plus the record spec § Middleware asks of the tmux door —
// subcommand, -t target, exit code and duration, written exactly once when
// the command completes (Run, Output, CombinedOutput, or Wait after Start).
// The embedded command keeps every field a façade sets (Stdin, Process), so
// the terminal is Command plus a record, never a second tmux runner.
//
// Only the SHAPE is recorded: a send-keys body is a prompt and a capture is
// a pane, so the arguments and the output never reach the file.
type Cmd struct {
	*exec.Cmd
	ctx     context.Context
	subcmd  string
	target  string
	clock   clock.Clock
	started time.Time
	done    bool
	// cancel releases the bound Exec added; nil when the caller set its own.
	cancel context.CancelFunc
	// runner runs the call instead of exec when the context carried one
	// (WithClientRunner); waited carries a runner-run Start's outcome to Wait.
	runner deps.Runner
	waited chan error
}

// defaultTimeout bounds a tmux call whose context carries no deadline: a
// stopped or wedged server accepts the connection and never answers, so an
// unbounded call hangs its caller and leaves its client blocked forever.
var defaultTimeout = 10 * time.Second

// waitDelay is how long a killed client may keep its pipes open: a tmux
// client hands its stdout to the server it talks to, so a client killed for
// a stopped server leaves that server holding the pipe and Wait would block
// on it forever. Past the delay the pipes are closed and the call returns.
const waitDelay = 500 * time.Millisecond

// Exec is Command with a completion record and a deadline: the same binary,
// socket argv and cleared TMUX, returned as a Cmd whose completing methods
// log once. A context with no deadline is bounded by defaultTimeout; a
// caller that needs longer passes a context carrying its own.
func Exec(ctx context.Context, binary, socketPath string, arguments ...string) *Cmd {
	return ExecWithin(ctx, defaultTimeout, binary, socketPath, arguments...)
}

// ExecWithin is Exec bounded by timeout when ctx carries no deadline of its
// own. On the deadline the client is killed and the call returns its error.
func ExecWithin(ctx context.Context, timeout time.Duration, binary, socketPath string, arguments ...string) *Cmd {
	var cancel context.CancelFunc
	if _, bounded := ctx.Deadline(); !bounded {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	if binary == "" && runnerFrom(ctx) != nil {
		binary = Binary // the runner resolves it, so its argv never names a host path
	}
	command := Command(ctx, binary, socketPath, arguments...)
	command.WaitDelay = waitDelay
	observed := Observe(ctx, command, arguments...)
	observed.cancel = cancel
	return observed
}

// ExecAttached is Exec for a call that lasts as long as its user keeps it —
// an interactive attach: the same record, never a default bound, so only the
// caller's own context ends it.
func ExecAttached(ctx context.Context, binary, socketPath string, arguments ...string) *Cmd {
	attached := Observe(ctx, Command(ctx, binary, socketPath, arguments...), arguments...)
	attached.runner = nil
	return attached
}

// TimedOut reports whether the call was ended at its deadline — the server
// did not answer in time — rather than by tmux's own exit: a caller names it
// "did not answer", never a bare "signal: killed".
func (command *Cmd) TimedOut() bool {
	return errors.Is(command.ctx.Err(), context.DeadlineExceeded)
}

// Observe is the completion record over a tmux command another launcher
// assembled from Invocation (spawn's durable systemd scope): arguments are
// the tmux arguments the shape is read from, never the launcher's own. The
// command runs through the runner ctx carries (WithClientRunner), if any.
func Observe(ctx context.Context, command *exec.Cmd, arguments ...string) *Cmd {
	subcmd, target := shape(arguments)
	return &Cmd{
		Cmd: command, ctx: ctx, subcmd: subcmd, target: target, clock: obs.Clock(ctx),
		runner: runnerFrom(ctx),
	}
}

// shape reads the subcommand (the first non-flag word) and the -t target
// out of a tmux argument list — the two fields the record carries.
func shape(arguments []string) (subcmd, target string) {
	for index, argument := range arguments {
		if subcmd == "" && argument != "" && argument[0] != '-' {
			subcmd = argument
		}
		if argument == "-t" && index+1 < len(arguments) {
			target = arguments[index+1]
		}
	}
	return subcmd, target
}

// Run completes the command and records it.
func (command *Cmd) Run() error {
	command.begin()
	err := command.refused()
	if err == nil && command.runner != nil {
		_, err = command.runThrough()
	} else if err == nil {
		err = command.Cmd.Run()
	}
	command.finish(err)
	return err
}

// Output completes the command and records it; the output is returned to
// the caller, never logged.
func (command *Cmd) Output() ([]byte, error) {
	return command.complete(false)
}

// CombinedOutput completes the command and records it.
func (command *Cmd) CombinedOutput() ([]byte, error) {
	return command.complete(true)
}

func (command *Cmd) complete(combined bool) (output []byte, err error) {
	command.begin()
	switch err = command.refused(); {
	case err != nil:
	case command.runner != nil:
		output, err = command.outputThrough(combined)
	case combined:
		output, err = command.Cmd.CombinedOutput()
	default:
		output, err = command.Cmd.Output()
	}
	command.finish(err)
	return output, err
}

// Start launches the command; the record is written by Wait, when the
// command completes — unless Start itself fails, which is the completion.
func (command *Cmd) Start() error {
	command.begin()
	err := command.refused()
	if err == nil && command.runner != nil {
		err = command.startThrough()
	} else if err == nil {
		err = command.Cmd.Start()
	}
	if err != nil {
		command.finish(err)
	}
	return err
}

// Wait completes a started command and records it.
func (command *Cmd) Wait() error {
	err := command.completion()
	command.finish(err)
	return err
}

func (command *Cmd) begin() {
	command.started = command.clock.Now()
}

// finish writes the one record with the exit code: a non-zero exit is tmux
// answering (INFO, err carried), exit -1 is a tmux that never started (ERROR,
// or INFO when the caller cancelled it: obs.CancelLevel), a call its deadline
// ended is WARN whichever error exec reports for it (TimedOut: a stopped or
// wedged server never answered — its caller names that server, as
// gather.ErrServerUnresponsive does, and pfm itself did not fail),
// and a clean exit is DEBUG unless it took obs.SlowCall or longer (INFO) —
// obs.RoutineLevel, the rule db.statement follows too. A second completion of
// the same Cmd writes nothing.
func (command *Cmd) finish(err error) {
	if command.done {
		return
	}
	command.done = true
	if command.cancel != nil {
		command.cancel()
	}
	exit, failure := 0, slog.LevelInfo
	var exitErr *exec.ExitError
	var runnerExit *ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	case errors.As(err, &runnerExit):
		exit = runnerExit.Code
	default:
		exit, failure = -1, obs.CancelLevel(err)
	}
	if err != nil && command.TimedOut() {
		failure = slog.LevelWarn
	}
	elapsed := command.clock.Now().Sub(command.started)
	attrs := []slog.Attr{
		slog.String("subcmd", command.subcmd),
		slog.Int(obs.FieldExit, exit),
		slog.Int64(obs.FieldDur, elapsed.Milliseconds()),
	}
	if command.target != "" {
		attrs = append(attrs, slog.String("target", command.target))
	}
	if err != nil {
		attrs = append(attrs, slog.String(obs.FieldErr, err.Error()))
	}
	scoped := obs.Component(command.ctx, comp)
	obs.Logger(scoped).LogAttrs(scoped, obs.RoutineLevel(err, failure, 0, elapsed), "tmux.exec", attrs...)
}
