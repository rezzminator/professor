package main

import (
	"context"
	"fmt"
	"io"

	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/cli"
)

// runInternalThen is the detached waiter behind chat_inject's `then`
// argument, mirroring chat.sh's __then subcommand: it rides out the primary
// turn and delivers the first steer once the pane has settled to idle,
// carrying the remainder so the chain re-arms one confirmed delivery at a
// time. It is spawned detached because a self-inject's waiter waits on the
// very turn that spawned it.
func runInternalThen(args []string, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(
		"internal then",
		"usage: pfm internal then --socket path --target name [--self] --steer text [--steer text]...",
		stderr,
	)
	socket := flags.String("socket", "", "tmux socket path of the target")
	target := flags.String("target", "", "tmux session name or pane id")
	var steers cli.StringList
	flags.Var(&steers, "steer", "follow-up steer; repeat for a chain")
	// Set only when the pane being watched is the pane that asked for the
	// wait, which is true for a chat compacting itself and false otherwise.
	// It decides whether the waiter must first let a turn it did NOT start
	// finish before it can recognise the primary's turn.
	selfTarget := flags.Bool("self", false, "the target pane is the caller's own")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *target == "" || len(steers) == 0 {
		flags.Usage()
		return 2
	}
	engine, err := pfmchat.NewInjectEngine(false, firstRuntime(runtimes))
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal then: %v\n", err)
		return 1
	}
	result, err := engine.DeliverThen(
		context.Background(),
		*socket,
		*target,
		steers,
		*selfTarget,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal then: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "then steer -> %s (code %d): %s\n",
		*target,
		result.Code,
		result.Message,
	)
	return result.Code
}
