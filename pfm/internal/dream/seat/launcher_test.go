package seat

import (
	"context"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/spawn"
)

type seatCodexLauncher struct{}

func (seatCodexLauncher) ComposerReady(capture string) bool { return spawn.CodexComposerReady(capture) }

func (seatCodexLauncher) Rename(
	ctx context.Context,
	tmux spawn.Tmux,
	socket, target, name string,
	timings spawn.Timings,
	trace spawn.Trace,
) (string, error) {
	return spawn.RenameCodex(ctx, tmux, socket, target, name, timings, trace)
}

func init() {
	spawn.RegisterLauncher(pfmengine.Codex, seatCodexLauncher{})
}
