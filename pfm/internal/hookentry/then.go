package hookentry

import (
	"context"
	"fmt"
	"io"
	"strings"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/inject"
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
// it is waiting for (inject.WaitingFor), instead of an empty file that reads identically for
// "waiting" and "never started".
func Then(args []string, stderr io.Writer, runtimes ...config.Runtime) int {
	flags := cli.NewFlagSet(
		"internal then",
		"usage: pfm internal then --socket path --target name [--engine cc|cx] --steer text [--steer text]...",
		stderr,
	)
	socket := flags.String("socket", "", "tmux socket path of the target")
	target := flags.String("target", "", "tmux session name or pane id")
	var steers cli.StringList
	flags.Var(&steers, "steer", "follow-up steer; repeat for a chain")
	engineName := flags.String("engine", "", "the target pane's engine id (cc|cx) as the spawning chat resolved it")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *target == "" || len(steers) == 0 {
		flags.Usage()
		return 2
	}
	// DeliverThen's Codex guard compares the canonical id, so an engine's long name
	// is resolved here and an unknown one refused before any wait.
	engineID := ""
	if strings.TrimSpace(*engineName) != "" {
		id, err := pfmengine.Parse(*engineName)
		if err != nil {
			fmt.Fprintf(stderr, "pfm internal then: --engine: %v\n", err)
			return 2
		}
		engineID = string(id)
	}
	fmt.Fprintf(
		stderr,
		"then waiter: start — target %s on %s · %d steer(s) · waiting for: %s\n",
		*target,
		*socket,
		len(steers),
		inject.WaitingFor(engineID),
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
		Engine:     engineID,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal then: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "then steer -> %s (code %d): %s\n", *target, result.Code, result.Message)
	return result.Code
}
