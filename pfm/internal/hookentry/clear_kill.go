package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/index"
)

// ClearKill is the fail-open /clear hook for Claude's SessionEnd event.
func ClearKill(args []string, stdin io.Reader, stderr io.Writer, runtimes ...config.Runtime) (exitCode int) {
	flags := cli.NewFlagSet("internal clear-kill", "usage: pfm internal clear-kill < hook-payload.json", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	payload, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: read hook payload (fail-open): %v\n", err)
		return 0
	}
	var hook struct {
		Event     string `json:"hook_event_name"`
		Reason    string `json:"reason"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(payload, &hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	if hook.SessionID == "" {
		return 0
	}
	if hook.Event != "SessionEnd" || hook.Reason != "clear" {
		return 0
	}
	database, manager, code := fleet.OpenKillManager(stderr, runtimes...)
	if code != 0 {
		fmt.Fprintln(stderr, "pfm internal clear-kill: store unavailable (fail-open)")
		return 0
	}
	defer func() {
		if err := database.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal clear-kill: close database (fail-open): %v\n", err)
		}
	}()
	ctx := context.Background()
	transcript, found, err := database.Transcript(ctx, hook.SessionID)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: resolve fleet session (fail-open): %v\n", err)
		return 0
	}
	if !found {
		return 0
	}
	runtime, runtimeErr := config.OptionalRuntime(runtimes)
	if runtimeErr != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: config unavailable (fail-open): %v\n", runtimeErr)
		return 0
	}
	indexer, err := index.NewWithRoots(database, runtime.Paths, runtime.Paths.Roots)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: prepare transcript refresh (fail-open): %v\n", err)
		return 0
	}
	if _, err := indexer.Run(ctx, index.Options{PriorityCWD: transcript.CWD, PriorityOnly: true}); err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: refresh transcript baseline (fail-open): %v\n", err)
		return 0
	}
	if _, found, err := manager.KillCleared(ctx, hook.SessionID); err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: record kill (fail-open): %v\n", err)
	} else if !found {
		return 0
	}
	return 0
}
