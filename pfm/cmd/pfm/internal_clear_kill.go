package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"hostops/pfm/internal/cli"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/index"
)

// runClearKill is the fail-open /clear hook for Claude's own
// SessionEnd(reason=clear): the hook payload names the completed session id
// directly. Codex has no equivalent hook wired anymore — its
// SessionStart(source=clear) fires on the new session's FIRST TURN, by which
// point every Codex chat on the host shares one app-server daemon pid, so the
// hook could never say which pane cleared. fleet.ReconcileCodexPanes
// replaces it: a gather pass reads each pane's own status line and detects
// the clear from there. Every unrelated or malformed payload returns 0
// without output.
func runClearKill(args []string, stdin io.Reader, stderr io.Writer, runtimes ...commandRuntime) (exitCode int) {
	flags := cli.NewFlagSet(
		"internal clear-kill",
		"usage: pfm internal clear-kill < hook-payload.json",
		stderr,
	)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	payload, err := io.ReadAll(stdin)
	if err != nil {
		return 0
	}
	var hook struct {
		Event     string `json:"hook_event_name"`
		Reason    string `json:"reason"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(payload, &hook) != nil || hook.SessionID == "" {
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
	runtime, runtimeErr := pfmconfig.OptionalRuntime(runtimes)
	if runtimeErr != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: config unavailable (fail-open): %v\n", runtimeErr)
		return 0
	}
	indexer, err := index.NewWithRoots(database, runtime.Paths, runtime.Paths.Roots)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal clear-kill: prepare transcript refresh (fail-open): %v\n", err)
		return 0
	}
	if _, err := indexer.Run(ctx, index.Options{
		PriorityCWD: transcript.CWD, PriorityOnly: true,
	}); err != nil {
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
