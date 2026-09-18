package hookentry

import (
	"context"
	"fmt"
	"io"

	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/inject"
)

// thenWaiter is the one engine verb Then drives, behind a seam so the start
// line can be tested against a waiter that never returns — the real waiter
// blocks for the whole turn it rides out.
type thenWaiter interface {
	DeliverThen(context.Context, inject.ThenWait) (inject.Result, error)
}

var newThenWaiter = func(runtime *config.Runtime) (thenWaiter, error) {
	return pfmchat.NewInjectEngine(false, runtime)
}

// Then is the detached waiter behind chat inject's follow-up steer chain.
//
// stderr IS the waiter's log (CommandThenSpawner.Spawn redirects both streams
// to the steer log), and the FIRST line on it goes down before anything can
// block: an operator reading the log while the waiter still waits sees what
// it is waiting for, in the same words the pane notice used
// (inject.WaitingFor), instead of an empty file that reads identically for
// "waiting" and "never started".
func Then(args []string, stderr io.Writer, runtimes ...config.Runtime) int {
	flags := cli.NewFlagSet(
		"internal then",
		"usage: pfm internal then --socket path --target name [--self] [--engine cc|cx] --steer text [--steer text]...",
		stderr,
	)
	socket := flags.String("socket", "", "tmux socket path of the target")
	target := flags.String("target", "", "tmux session name or pane id")
	var steers cli.StringList
	flags.Var(&steers, "steer", "follow-up steer; repeat for a chain")
	selfTarget := flags.Bool("self", false, "the target pane is the caller's own")
	engineName := flags.String("engine", "", "the target pane's engine id (cc|cx) as the spawning chat resolved it")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *target == "" || len(steers) == 0 {
		flags.Usage()
		return 2
	}
	fmt.Fprintf(
		stderr,
		"then waiter: start — target %s on %s · self=%t · %d steer(s) · waiting for: %s\n",
		*target,
		*socket,
		*selfTarget,
		len(steers),
		inject.WaitingFor(*selfTarget, *engineName),
	)
	var runtime *config.Runtime
	if len(runtimes) != 0 {
		runtime = &runtimes[0]
	}
	waiter, err := newThenWaiter(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal then: %v\n", err)
		return 1
	}
	result, err := waiter.DeliverThen(context.Background(), inject.ThenWait{
		SocketPath: *socket,
		Target:     *target,
		Steers:     steers,
		SelfTarget: *selfTarget,
		Engine:     *engineName,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal then: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "then steer -> %s (code %d): %s\n", *target, result.Code, result.Message)
	return result.Code
}
