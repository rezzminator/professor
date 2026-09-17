package hookentry

import (
	"context"
	"fmt"
	"io"

	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/config"
)

// Then is the detached waiter behind chat inject's follow-up steer chain.
func Then(args []string, stderr io.Writer, runtimes ...config.Runtime) int {
	flags := cli.NewFlagSet(
		"internal then",
		"usage: pfm internal then --socket path --target name [--self] --steer text [--steer text]...",
		stderr,
	)
	socket := flags.String("socket", "", "tmux socket path of the target")
	target := flags.String("target", "", "tmux session name or pane id")
	var steers cli.StringList
	flags.Var(&steers, "steer", "follow-up steer; repeat for a chain")
	selfTarget := flags.Bool("self", false, "the target pane is the caller's own")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *target == "" || len(steers) == 0 {
		flags.Usage()
		return 2
	}
	var runtime *config.Runtime
	if len(runtimes) != 0 {
		runtime = &runtimes[0]
	}
	engine, err := pfmchat.NewInjectEngine(false, runtime)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal then: %v\n", err)
		return 1
	}
	result, err := engine.DeliverThen(context.Background(), *socket, *target, steers, *selfTarget)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal then: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "then steer -> %s (code %d): %s\n", *target, result.Code, result.Message)
	return result.Code
}
