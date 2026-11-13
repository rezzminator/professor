package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/hookentry"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/themecmd"
)

// nameSyncHalves runs name-sync's two halves in order: on --apply the native
// half (every live Herdr seat named after its engine, pfmchat.ConvergeHerdrNames)
// first, then the window half (syncWindowNames), which a preview runs alone.
// The window half's exit code stands; a failed native half fails a run the
// window half passed.
func nameSyncHalves(apply bool, native func() bool, windows func() int) int {
	nativeOK := !apply || native()
	if code := windows(); code != 0 || nativeOK {
		return code
	}
	return 1
}

// syncWindowNames is name-sync's window half: every live tmux window, chat
// server option and theme converged, or with apply false only planned and
// printed. It exits 1 on a failure or an unverified rename, else 0.
func syncWindowNames(
	ctx context.Context,
	database *store.Store,
	runtime commandRuntime,
	apply bool,
	stdout, stderr io.Writer,
) int {
	environment, err := fleet.ResolveEnv(fleet.Request{Runtime: &runtime})
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}
	for _, fault := range environment.Faults {
		fmt.Fprintf(stderr, "pfm name-sync: %s\n", fault)
	}
	data, err := fleet.LoadData(ctx, database)
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}
	// Only an explicit --apply lets the gather pass perform its planned renames.
	live, err := fleet.Gather(ctx, database, environment, data, !apply, fleet.PrintWarn(stderr), stderr)
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: %v\n", err)
		return 1
	}
	if apply {
		fleet.ReconcileCodexPanes(ctx, database, live, runtime, fleet.PrintWarn(stderr))
	}
	verb := "renamed"
	if !apply {
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
	if !apply {
		// A preview reports its plan; it has no applied outcome to verify.
		fmt.Fprintf(stdout, "windows planned: %d\n", len(live.Renames))
		return 0
	}
	titlesTmux := gather.TmuxProbe{TmuxDir: environment.Paths.TmuxDir}
	titlesUnverified := convergeChatServerOptions(
		ctx, titlesTmux, liveSockets(live.Panes), environment.Config.Tmux.Titles,
		filepath.Join(environment.Paths.Home, ".local", "bin", "pfm"), stdout, stderr,
	)
	titlesUnverified += reannounceChatServers(
		ctx, hookentry.HerdrReannounce, environment.Paths.TmuxDir, liveSockets(live.Panes), stdout, stderr,
	)
	titlesUnverified += themecmd.ConvergeTmux(ctx, environment.Paths, liveSockets(live.Panes), stdout, stderr)
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
