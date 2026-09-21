package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/ask"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sky"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/stats"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/theme"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

const fourthEngineHelper = "PFM_FOURTH_ENGINE_HELPER"

func TestFourthEngineNeedsOnlyItsOwnPackage(t *testing.T) {
	if os.Getenv(fourthEngineHelper) != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestFourthEngineNeedsOnlyItsOwnPackage$")
		command.Env = append(os.Environ(), fourthEngineHelper+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("fourth-engine proof failed: %v\n%s", err, output)
		}
		return
	}

	id := pfmengine.ID("zz")
	pfmengine.Register(pfmengine.Descriptor{
		ID: id, Name: "Zed", Short: "Zed", LongName: "zed", Binary: "zed",
		SocketPrefix: "zz-", RootEnv: "PFM_ZZ_ROOT",
		DefaultRoots: func(home string) []string { return []string{filepath.Join(home, ".zed")} },
	})
	index.RegisterSource(id, fourthSource{})
	spawn.RegisterLauncher(id, fourthLauncher{})
	gather.RegisterMatcher(id, fourthMatcher{})
	stats.RegisterUsageSource(id, fourthUsageSource{})
	action.RegisterPlanner(id, fourthPlanner{})
	ask.RegisterRunner(id, fourthAskRunner{})

	parsed, err := pfmengine.Parse("zed")
	if err != nil || parsed != id {
		t.Fatalf("Parse(zed)=(%q,%v), want (%q,nil)", parsed, err, id)
	}
	if parsed, ok := pfmengine.FromSocket("zz-1"); !ok || parsed != id {
		t.Fatalf("FromSocket(zz-1)=(%q,%v), want (%q,true)", parsed, ok, id)
	}

	home := t.TempDir()
	root := filepath.Join(home, "zed-sessions")
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_ZZ_ROOT", root)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Roots[id]; len(got) != 1 || got[0] != root {
		t.Fatalf("Roots[zz]=%v, want [%s]", got, root)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	content := []byte(`{"version":2,"accounts":[],"ask":{"engine":"zed","zed":{"model":"m"}}}`)
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	machine, err := pfmconfig.Load(configPath, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if machine.Ask.Engine != id || machine.Ask.PrefsFor(id).Model != "m" {
		t.Fatalf("decoded ask config=%#v", machine.Ask)
	}

	var doctorOutput strings.Builder
	if warnings := doctor.PrintEngineCapabilities(
		&doctorOutput,
		doctor.Dependencies{ExpectedEngineCapabilities: expectedEngineCapabilities},
	); warnings != 0 {
		t.Fatalf("doctor warnings=%d row=%q", warnings, doctorOutput.String())
	}
	if !strings.Contains(doctorOutput.String(), "zz=index,launcher,matcher,usage,headless,ask") {
		t.Fatalf("doctor row omitted fourth engine: %q", doctorOutput.String())
	}

	current := pfmengine.Claude
	seen := false
	for range pfmengine.All() {
		current = ui.AdjacentEngine(current, 1)
		if current == id {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("picker cycle %v omitted %s", pfmengine.All(), id)
	}
	if rendered := sky.StarClass(id); rendered == "" {
		t.Fatal("fallback star rendered empty")
	}
	background := strings.SplitN(sky.Snapshot(0, 0), " ", 2)[0]
	if rendered := sky.StarClass(id); rendered == background {
		t.Fatalf("fourth engine star %q is the empty-fleet background star", rendered)
	}
	palette := theme.Load("default")
	if palette.EngineRow[id] == "" || palette.StatsEngine[id] == "" {
		t.Fatalf(
			"fourth engine has no theme fallback: rows=%q stats=%q",
			palette.EngineRow[id],
			palette.StatsEngine[id],
		)
	}
}

type fourthSource struct{}

func (fourthSource) Sync(context.Context, *store.Store, []string, *index.Counters) error { return nil }

type fourthLauncher struct{}

func (fourthLauncher) ComposerReady(string) bool { return true }

func (fourthLauncher) Rename(
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
