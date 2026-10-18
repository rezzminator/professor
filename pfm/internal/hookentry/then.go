package hookentry

import (
	"context"
	"fmt"
	"io"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// thenWaiter is the one engine verb Then drives, behind a seam so the start
// line can be tested against a waiter that never returns — the real waiter
// blocks for the whole turn it rides out.
type thenWaiter interface {
	DeliverThen(context.Context, pfmcall.ThenWait) (pfmcall.Result, error)
}

var newThenWaiter = func(runtime *config.Runtime) (thenWaiter, error) {
	return pfmchat.NewCallEngine(false, runtime)
}

// Then is the detached waiter behind chat call's follow-up steer chain.
//
// stderr IS the waiter's log (CommandThenSpawner.Spawn redirects both streams
// to the steer log), and the FIRST line on it goes down before anything can
// block: an operator reading the log while the waiter still waits sees what
// it is waiting for (pfmcall.WaitingFor), instead of an empty file that reads identically for
// "waiting" and "never started".
func Then(args []string, stderr io.Writer, runtimes ...config.Runtime) int {
	flags := cli.NewFlagSet(
		"internal then",
		"usage: pfm internal then --socket path --target name [--engine cc|cx] [--backend tmux|herdr] --steer text [--steer text]...",
		stderr,
	)
	socket := flags.String("socket", "", "socket path of the target seat")
	target := flags.String("target", "", "target session name or pane id")
	backend := flags.String("backend", "", "target seat backend (empty means tmux)")
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
	if *backend != "" && *backend != "tmux" && *backend != "herdr" {
		fmt.Fprintf(stderr, "pfm internal then: unsupported seat backend %q\n", *backend)
		return 2
	}
	if *backend == "herdr" && *socket == "" {
		fmt.Fprintln(stderr, "pfm internal then: Herdr backend requires an explicit socket")
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
		pfmcall.WaitingFor(engineID),
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
	result, err := waiter.DeliverThen(context.Background(), pfmcall.ThenWait{
		Backend:    *backend,
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
