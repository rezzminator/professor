package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/gather"
	fleetindex "hostops/pfm/internal/index"
	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/store"
)

// runNameSync converges every live chat's tmux WINDOW name — the fleet's DNS
// record. chat.sh resolves codex chats by it, the terminal tab renders it, and
// a person picking a pane reads it.
//
// A codex window follows its thread's indexed name; a claude window follows
// the 🔖 label its own statusline renders. Both halves are computed and
// applied by the same gather pass the picker runs, so there is exactly ONE
// writer of a window name however this command is reached — a systemd path
// unit on a codex rename, a timer, or a picker refresh.
func runNameSync(args []string, stdout, stderr io.Writer, runtime commandRuntime) (exitCode int) {
	flags := newFlagSet("name-sync", "usage: pfm name-sync [--dry-run]", stderr)
	dryRun := flags.Bool("dry-run", false, "report the renames without applying them")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	database, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}
	defer func() { closeCommandResource(database, "pfm name-sync: close database", stderr, &exitCode) }()
	ctx := context.Background()

	// A delta index first: a codex rename lands in session_index.jsonl or the
	// thread store, and the name a window converges on is read from the index.
	// Without this pass the sync would converge yesterday's names.
	indexer, err := fleetindex.NewWithRoots(database, runtime.Paths, runtime.Paths.Roots)
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}
	if _, err := indexer.Run(ctx, fleetindex.Options{}); err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}

	environment, err := fleet.ResolveEnv(fleet.Request{Runtime: &runtime})
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}
	data, err := fleet.LoadData(ctx, database)
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}
	// ReadOnly is what makes --dry-run a dry run: the gather pass applies the
	// renames it plans, and only a read-only pass plans without applying.
	live, err := fleet.Gather(ctx, database, environment, data,
		*dryRun,
		fleet.PrintWarn(stderr),
		stderr,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}
	if !*dryRun {
		fleet.ReconcileCodexPanes(ctx, database, live, runtime, fleet.PrintWarn(stderr))
	}
	verb := "renamed"
	if *dryRun {
		verb = "would rename"
	}
	for _, rename := range live.Renames {
		fmt.Fprintf(
			stdout,
			"%s %s %s: %s -> %s\n",
			verb,
			rename.Socket,
			rename.WindowID,
			rename.CurrentName,
			rename.TargetName,
		)
	}
	if *dryRun {
		// A dry run applied nothing, so it has nothing to verify. It reports
		// the PLAN, and says so — a plan counted as an outcome is exactly the
		// lie this command used to tell.
		fmt.Fprintf(stdout, "windows planned: %d\n", len(live.Renames))
		return 0
	}
	titlesTmux := gather.CommandTmux{TmuxTmpDir: filepath.Dir(environment.Paths.TmuxDir)}
	titlesUnverified := convergeChatServerOptions(
		ctx,
		titlesTmux,
		liveSockets(live.Panes),
		environment.Config.Tmux.Titles,
		stdout,
		stderr,
	)
	if titlesUnverified != 0 {
		fmt.Fprintf(stdout, "tmux options unverified: %d\n", titlesUnverified)
	}
	converged, unverified := verifyRenames(ctx, runtime, live.Renames, stderr)
	fmt.Fprintf(stdout, "windows converged: %d\n", converged)
	if unverified != 0 {
		fmt.Fprintf(stdout, "windows unverified: %d\n", unverified)
	}
	if titlesUnverified != 0 || unverified != 0 {
		return 1
	}
	return 0
}

// verifyRenames reads every renamed window's name BACK off its server and
// counts only a match as converged.
//
// An attempt is not an outcome. `windows converged: N` used to count the
// renames this pass planned, so a window whose name a second writer took back
// — or one whose server died between the plan and the rename — was reported as
// converged while the fleet still could not address it by that name. Each
// unverified window is named with the value actually read, and the command
// exits non-zero so a scheduler run that achieved nothing is not silent.
func verifyRenames(
	ctx context.Context,
	runtime commandRuntime,
	renames []gather.WindowRename,
	stderr io.Writer,
) (converged, unverified int) {
	reader := inject.CommandTmux{}
	for _, rename := range renames {
		socketPath := filepath.Join(runtime.Paths.TmuxDir, rename.Socket)
		actual, err := reader.WindowName(ctx, socketPath, rename.WindowID)
		if err != nil {
			unverified++
			fmt.Fprintf(
				stderr,
				"pfm name-sync: window %s %s: wanted %q, could not be read back after rename: %v\n",
				rename.Socket, rename.WindowID, rename.TargetName, err,
			)
			continue
		}
		if actual != rename.TargetName {
			unverified++
			fmt.Fprintf(
				stderr,
				"pfm name-sync: window %s %s: wanted %q, reads %q after rename\n",
				rename.Socket, rename.WindowID, rename.TargetName, actual,
			)
			continue
		}
		converged++
	}
	return converged, unverified
}

// liveSockets returns the DISTINCT sockets backing panes, sorted. name-sync
// already probed these servers to plan window renames; titles convergence
// reuses that same enumeration rather than probing the tmux directory a
// second time.
func liveSockets(panes []gather.Pane) []string {
	seen := make(map[string]bool, len(panes))
	sockets := make([]string, 0, len(panes))
	for index := range panes {
		pane := &panes[index]
		if seen[pane.Socket] {
			continue
		}
		seen[pane.Socket] = true
		sockets = append(sockets, pane.Socket)
	}
	sort.Strings(sockets)
	return sockets
}

// convergeChatServerOptions is the ONE place an EXISTING live server is
// brought onto pfmconfig.ChatServerOptions — the list the one chat-server
// creator (spawn.CommandTmux.NewSession) applies at birth — so a server that
// predates a policy change, one a scheduler outage left behind, or one born
// before its door went through the creator converges on the next scheduled
// name-sync. A HOST-owned title policy contributes no title option, so a host
// that owns its own OSC title keeps it; automatic-rename off is never gated.
//
// Every server on a pfm-owned title policy then gets the flip-and-restore
// nudge tmux-title-renudge performs, so a terminal caching a stale title (a
// VS Code tab reviving a persistent pane) repaints even when no option
// changed. Each converged server is named with its transitions, a read or
// apply failure is named on stderr without stopping the pass, and one summary
// count closes it.
func convergeChatServerOptions(
	ctx context.Context,
	tmux gather.CommandTmux,
	sockets []string,
	titles pfmconfig.TmuxTitles,
	stdout, stderr io.Writer,
) (unverified int) {
	converged := 0
	for _, socket := range sockets {
		transitions, err := tmux.ConvergeGlobalOptions(ctx, socket, pfmconfig.ChatServerOptions(&titles))
		if err != nil {
			fmt.Fprintf(stderr, "pfm name-sync: tmux options %s: %v\n", socket, err)
			unverified++
			continue
		}
		if len(transitions) != 0 {
			fmt.Fprintf(stdout, "tmux options %s converged: %s\n", socket, strings.Join(transitions, ", "))
			converged++
		}
		if !titles.Enabled {
			continue
		}
		if err := tmux.NudgeTitlesString(ctx, socket, pfmconfig.TmuxTitlesString); err != nil {
			fmt.Fprintf(stderr, "pfm name-sync: tmux titles %s: %v\n", socket, err)
			unverified++
		}
	}
	fmt.Fprintf(stdout, "tmux options converged: %d\n", converged)
	return unverified
}
