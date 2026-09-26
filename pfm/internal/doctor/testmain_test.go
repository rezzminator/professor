package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/ask"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	codexengine "github.com/rezzminator/professor/pfm/internal/engine/codex"
	opencodeengine "github.com/rezzminator/professor/pfm/internal/engine/opencode"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/stats"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const harnessPromptFixtureCaptured = "pfm jail fixture harness prompt\n"

type commandRuntime = config.Runtime

var harnessPromptModels = HarnessPromptModels

func expectedEngineCapabilities(id engine.ID, all []string) map[string]bool {
	if id == engine.OpenCode {
		return map[string]bool{"index": true, "matcher": true}
	}
	result := make(map[string]bool, len(all))
	for _, capability := range all {
		result[capability] = true
	}
	return result
}

func testDependencies() Dependencies {
	return Dependencies{ExpectedEngineCapabilities: expectedEngineCapabilities}
}

func runDoctor(args []string, stdout, stderr interface{ Write([]byte) (int, error) }, runtime config.Runtime) int {
	return Run(args, stdout, stderr, runtime, testDependencies())
}

func stageModelHarnessPromptBaseline(
	t *testing.T,
	home string,
	model HarnessPromptModel,
	captured, name string,
) {
	t.Helper()
	testjail.StageHarnessPromptBaseline(t, home, model.Alias, model.Stem, captured, name)
}

func stageHarnessPromptBaseline(t *testing.T, home string) {
	t.Helper()
	for _, model := range HarnessPromptModels {
		stageModelHarnessPromptBaseline(t, home, model, harnessPromptFixtureCaptured, "harness-prompt-fixture.md")
	}
}

func jailTest(t *testing.T) string {
	t.Helper()
	root := testjail.InstalledHome(t)
	stageHarnessPromptBaseline(t, filepath.Join(root, "home"))
	return root
}

type noNetworkHarvestDoctor struct{}

func (noNetworkHarvestDoctor) Inspect(string, harvestpy.Platform) (harvestpy.EnvironmentDigest, error) {
	return noNetworkHarvestDigest(), nil
}

func (noNetworkHarvestDoctor) Check(context.Context, string, harvestpy.Platform) (harvestpy.CheckReport, error) {
	digest := noNetworkHarvestDigest()
	return harvestpy.CheckReport{Healthy: true, Digest: digest, Checks: map[string]harvestpy.CheckStatus{
		"interpreter": {OK: true}, "lock_completeness": {OK: true},
		"live_smoke": {OK: true}, "live_smoke_conversion": {OK: true},
	}}, nil
}

func noNetworkHarvestDigest() harvestpy.EnvironmentDigest {
	return harvestpy.EnvironmentDigest{
		Schema: 1, Python: "3.11.15+20260610", LockSHA256: "test-lock-digest",
		InventorySHA256: "test-inventory-digest", InventoryCount: 1,
	}
}

func registerTestEngines() {
	index.RegisterSource(engine.Claude, claudeengine.Source{})
	index.RegisterSource(engine.Codex, codexengine.Source{})
	index.RegisterSource(engine.OpenCode, opencodeengine.Source{})
	spawn.RegisterLauncher(engine.Claude, claudeengine.Launcher{})
	spawn.RegisterLauncher(engine.Codex, codexengine.Launcher{})
	gather.RegisterMatcher(engine.Claude, claudeengine.Matcher{})
	gather.RegisterMatcher(engine.Codex, codexengine.Matcher{})
	gather.RegisterMatcher(engine.OpenCode, opencodeengine.Matcher{})
	stats.RegisterUsageSource(engine.Claude, claudeengine.UsageSource{})
	stats.RegisterUsageSource(engine.Codex, codexengine.UsageSource{})
	action.RegisterPlanner(engine.Claude, claudeengine.HeadlessPlanner{})
	action.RegisterPlanner(engine.Codex, codexengine.HeadlessPlanner{})
	ask.RegisterRunner(engine.Claude, claudeengine.AskRunner{})
	ask.RegisterRunner(engine.Codex, codexengine.AskRunner{})
}

func TestMain(m *testing.M) {
	registerTestEngines()
	HarvestOverride = noNetworkHarvestDoctor{}
	DaemonReachabilityOverride = func(config.Runtime) (mcpserv.DaemonStatus, error) {
		return mcpserv.DaemonStatus{}, fmt.Errorf("%w: jailed test never probes a live daemon", mcpserv.ErrDaemonAbsent)
	}
	PrePushGateProbeOverride = func(context.Context) PrePushGate { return PrePushGate{State: "outside-repository"} }
	DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
		results := make([]deps.Result, 0, len(entries))
		for index := range entries {
			entry := entries[index]
			if !entry.AppliesTo(goRuntime.GOOS) {
				results = append(
					results,
					deps.Result{Entry: entry, State: deps.StateSkipped, Error: "not this platform"},
				)
				continue
			}
			results = append(results, deps.Result{
				Entry: entry, State: deps.StateOK,
				Path: "/test/bin/" + entry.Name, Version: entry.MinVersion,
			})
		}
		return results
	}
	installer.HookProbeOverride = func(home string, machine config.Config) []installer.HookProbeResult {
		expected := installer.ExpectedHooks(home, machine)
		results := make([]installer.HookProbeResult, 0, len(expected))
		for _, hook := range expected {
			results = append(results, installer.HookProbeResult{Hook: hook, State: "ok"})
		}
		return results
	}
	HarnessCaptureOverride = func(_ context.Context, _ string, _ config.Config, alias, _ string) (HarnessCapture, error) {
		return HarnessCapture{
			Prompt:        harnessPromptFixtureCaptured,
			ResolvedModel: "claude-" + alias + "-5",
			CLIVersion:    "fixture",
		}, nil
	}
	os.Exit(testjail.Run(m))
}

type fourthSource struct{}

func (fourthSource) Sync(context.Context, *store.Store, []string, *index.Counters) error { return nil }

type fourthMatcher struct{}

func (fourthMatcher) IsCommand(argv []string, _ ...string) bool {
	return len(argv) != 0 && filepath.Base(argv[0]) == "zed"
}
