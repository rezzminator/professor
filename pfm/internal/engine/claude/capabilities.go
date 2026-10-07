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

// Rename types nothing at boot: a Claude chat's name rides --name on its launch
// argv. spawn.Run's Claude callers both plan it there — `pfm chat new` through
// action.PlanClaude, `pfm chat branch` through action.HeadlessFork — so a
// /rename typed here would only repeat it into a composer still settling. A
// live rename goes through `pfm chat name` or MCP chat_name, which inject
// /rename (internal/chat/target.go DeliverName) and read it back
// (cmd/pfm/chat_name_confirm.go).
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
