package testjail

import (
	"context"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TmuxRunner is a deps.FakeRunner playing a tmux that answers every call with
// result, for a test to hand the tmux façade through tmux.WithClientRunner,
// which gives it tmux calls alone: its script matches any argv.
func TmuxRunner(result deps.RunResult) *deps.FakeRunner {
	fake := &deps.FakeRunner{}
	fake.Script(nil, result, nil)
	return fake
}

// HungTmuxRunner is a deps.FakeRunner playing a tmux whose server never
// answers a list-panes, for a test to hand the tmux façade through
// tmux.WithClientRunner: the call waits until its caller's deadline, the way a
// stopped or wedged server leaves a client waiting on it. kill-server exits
// killServerRC; every other subcommand exits 1 naming itself on stderr.
func HungTmuxRunner(killServerRC int) *deps.FakeRunner {
	fake := &deps.FakeRunner{}
	fake.Handle(nil, func(ctx context.Context, call deps.RunCall) (deps.RunResult, error) {
		switch {
		case slices.Contains(call.Argv, "list-panes"):
			<-ctx.Done()
			return deps.RunResult{ExitCode: -1}, ctx.Err()
		case slices.Contains(call.Argv, "kill-server"):
			return deps.RunResult{ExitCode: killServerRC}, nil
		}
		return deps.RunResult{
			Stderr:   []byte("hung tmux fake: unexpected " + strings.Join(call.Argv[1:], " ") + "\n"),
			ExitCode: 1,
		}, nil
	})
	return fake
}

// AbsentTmuxRunner is a deps.FakeRunner playing a tmux with no server on any
// socket, for a test to hand the tmux façade through tmux.WithClientRunner:
// every call exits 1 with tmux's own "no server running on {socket}".
func AbsentTmuxRunner() *deps.FakeRunner {
	fake := &deps.FakeRunner{}
	fake.Handle(nil, func(_ context.Context, call deps.RunCall) (deps.RunResult, error) {
		socket := ""
		if index := slices.Index(call.Argv, "-S"); index >= 0 && index+1 < len(call.Argv) {
			socket = call.Argv[index+1]
		}
		return deps.RunResult{Stderr: []byte("no server running on " + socket + "\n"), ExitCode: 1}, nil
	})
	return fake
}
