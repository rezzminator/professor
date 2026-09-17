package fleet

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/naming"
	"hostops/pfm/internal/store"
)

// Warn reports one tmux probe warning raised during a gather pass. One-shot
// callers print immediately through PrintWarn; the interactive picker buffers
// instead, because Bubble Tea owns the tty while it runs and a warning written
// straight to stderr mid-refresh lands on top of its alt-screen frame.
type Warn func(warning string)

// PrintWarn reports a warning immediately on stderr.
func PrintWarn(stderr io.Writer) Warn {
	return func(warning string) {
		fmt.Fprintf(stderr, "pfm: tmux probe warning: %s\n", warning)
	}
}

// KillDependencies is the kill manager's view of the runtime: the fleet's
// paths, both engines' roots, and the config the exit choreography reads.
func KillDependencies(runtime pfmconfig.Runtime) kill.Dependencies {
	return kill.Dependencies{
		Paths:       runtime.Paths,
		ClaudeRoots: append([]string(nil), runtime.Paths.Roots[pfmengine.Claude]...),
		CodexHomes:  runtime.Config.CodexHomes(),
		ConfigPath:  runtime.Config.Path,
	}
}

// Gather probes every live tmux pane and engine process against the loaded
// data. Unless readOnly, it also applies the window renames the probe asks
// for; a rename failure is reported on stderr and never fails the pass.
func Gather(
	ctx context.Context,
	database *store.Store,
	env Env,
	data Data,
	readOnly bool,
	warn Warn,
	stderr io.Writer,
) (gather.Snapshot, error) {
	codexNamesByPath, codexNamesByID := naming.CodexNameIndex(
		store.CodexThreads(data.Rollouts),
		data.CxNames,
	)
	tmuxClient := gather.TmuxProbe{
		TmuxTmpDir: filepath.Dir(env.Paths.TmuxDir),
	}
	// The pane-binding manager lets the rollout-less live-process resolver
	// (store.NewCodexThreadResolverRoots) rank a pane's fleet-recorded thread
	// binding over its own birth-window guess — the guess never moves once a
	// pane clears, since the pane's TUI process is not restarted.
	bindingManager, err := kill.New(database, KillDependencies(env.Runtime()))
	if err != nil {
		return gather.Snapshot{}, fmt.Errorf("prepare Codex pane binding resolver: %w", err)
	}
	gatherer, err := gather.New(gather.Dependencies{
		Tmux:       tmuxClient,
		TmuxTmpDir: filepath.Dir(env.Paths.TmuxDir),
		CodexName: func(rolloutPath string) string {
			return codexNamesByPath[filepath.Clean(rolloutPath)]
		},
		CodexIDName: func(threadID string) string {
			return codexNamesByID[threadID]
		},
		CodexThread: store.NewCodexThreadResolverRoots(
			ctx, env.Config.CodexHomes(), bindingManager.CodexPaneBound(ctx),
		),
		CodexHomes:   env.Config.CodexHomes(),
		ClaudeBinary: env.Config.Claude.Binary,
		CodexBinary:  env.Config.Codex.Binary,
		LabelEmojis:  env.Config.LabelEmojis(),
		ReadOnly:     readOnly,
	})
	if err != nil {
		return gather.Snapshot{}, err
	}
	live, err := gatherer.Gather(ctx)
	if err != nil {
		return gather.Snapshot{}, err
	}
	for _, warning := range live.Warnings {
		warn(warning)
	}
	if !readOnly {
		for _, rename := range live.Renames {
			if err := tmuxClient.RenameWindow(ctx, rename); err != nil {
				fmt.Fprintf(stderr, "pfm: %v\n", err)
				continue
			}
			for index := range live.Panes {
				if live.Panes[index].Socket == rename.Socket &&
					live.Panes[index].WindowID == rename.WindowID {
					live.Panes[index].WindowName = rename.TargetName
				}
			}
		}
	}
	return live, nil
}
