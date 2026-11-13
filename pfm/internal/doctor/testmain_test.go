package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strings"
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

// runDoctor runs the doctor over healthyDoctorRunner; any other probe through
// the doctor's runner answers as an unscripted call, never as a process.
func runDoctor(args []string, stdout, stderr interface{ Write([]byte) (int, error) }, runtime config.Runtime) int {
	dependencies := testDependencies()
	dependencies.Runner = healthyDoctorRunner()
	return Run(args, stdout, stderr, runtime, dependencies)
}

// healthyDoctorRunner is the FakeRunner holding the one answer a healthy jail
// needs: python3 and the stub kit.
func healthyDoctorRunner() *deps.FakeRunner {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("python3", tracerPython, nil)
	fake.Script([]string{tracerPython, "--version"}, deps.RunResult{Stdout: []byte("Python 3.12.1\n")}, nil)
	fake.Script([]string{tracerPython}, tracerUsage, nil)
	return fake
}

// childEnvValue is the value a launched child's environment holds for name; the
// last entry wins, as it does for a launched process.
func childEnvValue(env []string, name string) (string, bool) {
	value, found := "", false
	for _, entry := range env {
		if rest, ok := strings.CutPrefix(entry, name+"="); ok {
			value, found = rest, true
		}
	}
	return value, found
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
	dirs, files := storeLayout()
	testjail.StageAccountLinks(t, filepath.Join(root, "home"), filepath.Join(root, "home", ".cc", "1"), dirs, files)
	stageStorePlugins(t, filepath.Join(root, "home"))
	testjail.StageHerdrClaudeHook(t, filepath.Join(root, "home", ".cc", "1"))
	testjail.PinClaudeAsk(t, filepath.Join(root, "home"))
	return root
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
	DaemonReachabilityOverride = func(config.Runtime) (mcpserv.DaemonStatus, error) {
		return mcpserv.DaemonStatus{}, fmt.Errorf("%w: jailed test never probes a live daemon", mcpserv.ErrDaemonAbsent)
	}
	PrePushGateProbeOverride = func(context.Context) PrePushGate { return PrePushGate{State: "outside-repository"} }
	// The jail reaches no user service manager: macOS always has launchctl,
	// whose real gui domain holds no plist in a jailed home.
	ScheduleTimerProbeOverride = func(context.Context, deps.Runner) ServiceManagerReport { return ServiceManagerReport{} }
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
	installer.HookProbeOverride = func(string, config.Config) []installer.HookProbeResult { return nil }
	HarnessCaptureOverride = func(_ context.Context, _ string, _ config.Config, alias, _ string) (HarnessCapture, error) {
		prompt, err := testjail.ShippedHarnessPrompt(alias)
		return HarnessCapture{
			Prompt:        prompt,
			ResolvedModel: "claude-" + alias + "-5",
			CLIVersion:    "fixture",
		}, err
	}
	os.Exit(testjail.Run(m))
}

type fourthSource struct{}

func (fourthSource) Sync(context.Context, *store.Store, []string, *index.Counters) error { return nil }

type fourthMatcher struct{}

func (fourthMatcher) IsCommand(argv []string, _ ...string) bool {
	return len(argv) != 0 && filepath.Base(argv[0]) == "zed"
}

func storeLayout() ([]string, map[string]string) {
	var names []string
	files := make(map[string]string)
	for _, entry := range installer.StoreEntries {
		if entry.Dir {
			names = append(names, entry.Name)
		} else {
			files[entry.Name] = entry.Seed
		}
	}
	return names, files
}

func stageStorePlugins(t *testing.T, home string) {
	t.Helper()
	storeDir := installer.ClaudeStore(home)
	ids, err := installer.ClaudePluginsNotInstalled(
		t.TempDir(),
		installer.ClaudePluginTargets(installer.ClaudePluginBuild{Plugins: config.DefaultClaudePlugins()}),
	)
	if err != nil {
		t.Fatal(err)
	}
	testjail.StageClaudePlugins(t, storeDir, ids)
	testjail.StageTracetreeKit(t, storeDir, ids)
}
