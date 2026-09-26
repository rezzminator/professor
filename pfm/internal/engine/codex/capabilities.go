package codex

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/ask"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/stats"
	"github.com/rezzminator/professor/pfm/internal/store"
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
