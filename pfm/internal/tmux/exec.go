package tmux

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
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
}

// Exec is Command with a completion record: the same binary, socket argv
// and cleared TMUX, returned as a Cmd whose completing methods log once.
func Exec(ctx context.Context, binary, socketPath string, arguments ...string) *Cmd {
	return Observe(ctx, Command(ctx, binary, socketPath, arguments...), arguments...)
}

// Observe is the completion record over a tmux command another launcher
// assembled from Invocation (spawn's durable systemd scope): arguments are
// the tmux arguments the shape is read from, never the launcher's own.
func Observe(ctx context.Context, command *exec.Cmd, arguments ...string) *Cmd {
	subcmd, target := shape(arguments)
	return &Cmd{Cmd: command, ctx: ctx, subcmd: subcmd, target: target, clock: obs.Clock(ctx)}
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
	err := command.Cmd.Run()
	command.finish(err)
	return err
}

// Output completes the command and records it; the output is returned to
// the caller, never logged.
func (command *Cmd) Output() ([]byte, error) {
	command.begin()
	output, err := command.Cmd.Output()
	command.finish(err)
	return output, err
}

// CombinedOutput completes the command and records it.
func (command *Cmd) CombinedOutput() ([]byte, error) {
	command.begin()
	output, err := command.Cmd.CombinedOutput()
	command.finish(err)
	return output, err
}

// Start launches the command; the record is written by Wait, when the
// command completes — unless Start itself fails, which is the completion.
func (command *Cmd) Start() error {
	command.begin()
	err := command.Cmd.Start()
	if err != nil {
		command.finish(err)
	}
	return err
}

// Wait completes a started command and records it.
func (command *Cmd) Wait() error {
	err := command.Cmd.Wait()
	command.finish(err)
	return err
}

func (command *Cmd) begin() {
	command.started = command.clock.Now()
}

// finish writes the one record: INFO with the exit code when tmux ran (a
// non-zero exit is tmux answering, err carried), ERROR with exit -1 when it
// never started. A second completion of the same Cmd writes nothing.
func (command *Cmd) finish(err error) {
	if command.done {
		return
	}
	command.done = true
	exit, level := 0, slog.LevelInfo
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	default:
		exit, level = -1, slog.LevelError
	}
	attrs := []slog.Attr{
		slog.String("subcmd", command.subcmd),
		slog.Int(obs.FieldExit, exit),
		slog.Int64(obs.FieldDur, command.clock.Now().Sub(command.started).Milliseconds()),
	}
	if command.target != "" {
		attrs = append(attrs, slog.String("target", command.target))
	}
	if err != nil {
		attrs = append(attrs, slog.String(obs.FieldErr, err.Error()))
	}
	scoped := obs.Component(command.ctx, comp)
	obs.Logger(scoped).LogAttrs(scoped, level, "tmux.exec", attrs...)
}
