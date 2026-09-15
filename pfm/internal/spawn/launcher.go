package spawn

import (
	"context"
	"fmt"
	"sort"

	pfmengine "hostops/pfm/internal/engine"
)

type Launcher interface {
	ComposerReady(capture string) bool
	Rename(
		ctx context.Context,
		tmux Tmux,
		socket, target, name string,
		timings Timings,
		trace Trace,
	) (warning string, err error)
}

var launchers = map[pfmengine.ID]Launcher{}

func RegisterLauncher(id pfmengine.ID, launcher Launcher) {
	if _, duplicate := launchers[id]; duplicate {
		panic(fmt.Sprintf("spawn: launcher for engine %q registered twice", id))
	}
	launchers[id] = launcher
}

func LauncherFor(id pfmengine.ID) (Launcher, error) {
	launcher, ok := launchers[id]
	if !ok {
		return nil, fmt.Errorf("engine %s: no launcher registered", id)
	}
	return launcher, nil
}

func RegisteredLaunchers() []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(launchers))
	for id := range launchers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}

func CodexComposerReady(capture string) bool { return composerReady(capture) }

func RenameCodex(
	ctx context.Context,
	tmux Tmux,
	socket, target, name string,
	timings Timings,
	trace Trace,
) (string, error) {
	named, warning, blocked := nameCodexThread(ctx, tmux, socket, target, name, timings, trace, codexRenameProof())
	if blocked && warning == "" {
		warning = "composer unavailable; thread was not renamed"
	}
	if !named && warning == "" {
		warning = "thread rename was not confirmed"
	}
	return warning, nil
}
