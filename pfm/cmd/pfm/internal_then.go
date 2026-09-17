package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// steerList collects a repeated --steer flag in the order it was given, so a
// chain delivers in the order the caller wrote it.
type steerList []string

func (list *steerList) String() string {
	return fmt.Sprintf("%v", []string(*list))
}

func (list *steerList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("a then steer must be non-empty")
	}
	*list = append(*list, value)
	return nil
}

// runInternalThen is the detached waiter behind chat_inject's `then`
// argument, mirroring chat.sh's __then subcommand: it rides out the primary
// turn and delivers the first steer once the pane has settled to idle,
// carrying the remainder so the chain re-arms one confirmed delivery at a
// time. It is spawned detached because a self-inject's waiter waits on the
// very turn that spawned it.
func runInternalThen(args []string, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		"internal then",
		"usage: pfm internal then --socket path --target name [--self] --steer text [--steer text]...",
		stderr,
	)
	socket := flags.String("socket", "", "tmux socket path of the target")
	target := flags.String("target", "", "tmux session name or pane id")
	var steers steerList
	flags.Var(&steers, "steer", "follow-up steer; repeat for a chain")
	// Set only when the pane being watched is the pane that asked for the
	// wait, which is true for a chat compacting itself and false otherwise.
	// It decides whether the waiter must first let a turn it did NOT start
	// finish before it can recognise the primary's turn.
	selfTarget := flags.Bool("self", false, "the target pane is the caller's own")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *target == "" || len(steers) == 0 {
		flags.Usage()
		return 2
	}
	engine, err := newInjectEngine(runtimes...)
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

var _ flag.Value = (*steerList)(nil)
