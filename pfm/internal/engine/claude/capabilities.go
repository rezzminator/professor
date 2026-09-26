package claude

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

// Source is Claude's index capability. The index package retains the shared
// incremental transaction while this seam makes capability presence explicit.
type Source struct{}

func (Source) Sync(ctx context.Context, database *store.Store, roots []string, counters *index.Counters) error {
	return index.SyncClaude(ctx, database, roots, counters)
}

type Launcher struct{}

func (Launcher) ComposerReady(string) bool { return true }

func (Launcher) Rename(
	context.Context,
	spawn.Tmux,
	string,
	string,
	string,
	spawn.Timings,
	spawn.Trace,
) (string, error) {
	return "", nil
}

type UsageSource struct{}

func (UsageSource) Fetch(ctx context.Context, account stats.LimitAccount) (stats.AccountLimits, error) {
	return stats.FetchClaude(ctx, account)
}

type HeadlessPlanner struct{}

func (HeadlessPlanner) Plan(request action.HeadlessRequest) (action.HeadlessPlan, error) {
	return action.PlanClaude(request)
}

type AskRunner struct{}

func (AskRunner) Resolve(machine pfmconfig.Config) (ask.Engine, error) {
	return ask.ResolveClaude(machine)
}
