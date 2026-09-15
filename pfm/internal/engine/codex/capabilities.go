package codex

import (
	"context"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/ask"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/stats"
	"hostops/pfm/internal/store"
)

type Source struct{}

func (Source) Sync(ctx context.Context, database *store.Store, roots []string, counters *index.Counters) error {
	return index.SyncCodex(ctx, database, roots, counters)
}

type Launcher struct{}

func (Launcher) ComposerReady(capture string) bool { return spawn.CodexComposerReady(capture) }

func (Launcher) Rename(
	ctx context.Context,
	tmux spawn.Tmux,
	socket, target, name string,
	timings spawn.Timings,
	trace spawn.Trace,
) (string, error) {
	return spawn.RenameCodex(ctx, tmux, socket, target, name, timings, trace)
}

type UsageSource struct{}

func (UsageSource) Fetch(ctx context.Context, account stats.LimitAccount) (stats.AccountLimits, error) {
	return stats.FetchCodex(ctx, account)
}

type HeadlessPlanner struct{}

func (HeadlessPlanner) Plan(request action.HeadlessRequest) (action.HeadlessPlan, error) {
	return action.PlanCodex(request)
}

type AskRunner struct{}

func (AskRunner) Resolve(machine pfmconfig.Config) (ask.Engine, error) {
	return ask.ResolveCodex(machine)
}
