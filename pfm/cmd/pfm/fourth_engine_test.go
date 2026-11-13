package main

import (
	"context"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/ask"
	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/stats"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const fourthEngineHelper = "PFM_FOURTH_ENGINE_HELPER"

type fourthSource struct{}

func (fourthSource) Sync(context.Context, *store.Store, []string, *index.Counters) error { return nil }

type fourthLauncher struct{}

func (fourthLauncher) ComposerReady(string) bool { return true }

func (fourthLauncher) Rename(
	context.Context,
	pfmcall.Screen,
	pfmcall.Target,
	string,
	spawn.Timings,
	spawn.Trace,
) (string, error) {
	return "", nil
}

type fourthMatcher struct{}

func (fourthMatcher) IsCommand(argv []string, _ ...string) bool {
	return len(argv) != 0 && filepath.Base(argv[0]) == "zed"
}

type fourthUsageSource struct{}

func (fourthUsageSource) Fetch(context.Context, stats.LimitAccount) (stats.AccountLimits, error) {
	return stats.AccountLimits{}, nil
}

type fourthPlanner struct{}

func (fourthPlanner) Plan(action.HeadlessRequest) (action.HeadlessPlan, error) {
	return action.HeadlessPlan{}, nil
}

type fourthAskRunner struct{}

func (fourthAskRunner) Resolve(pfmconfig.Config) (ask.Engine, error) { return fourthAskEngine{}, nil }

type fourthAskEngine struct{}

func (fourthAskEngine) Run(context.Context, ask.AskInput) (ask.AskResult, error) {
	return ask.AskResult{}, nil
}
