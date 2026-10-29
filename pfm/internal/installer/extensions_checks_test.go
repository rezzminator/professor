package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestExtensionChecksNameEveryDriftAndUnreadable(t *testing.T) {
	inst := extensionFixture(t)
	checks, err := ExtensionChecks(context.Background(), inst.managedRoot, inst.options)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, check := range checks {
		if check.Problem != "" {
			found[check.Engine+"/"+check.Aspect] = true
		}
	}
	for _, want := range []string{"claude/installed", "claude/enabled", "claude/options", "claude/env", "opencode/installed", "opencode/enabled", "opencode/env"} {
		if !found[want] {
			t.Errorf("drift %s not named: %+v", want, checks)
		}
	}
	if err := os.WriteFile(
		filepath.Join(inst.options.PrimaryConfigDir, "settings.json"),
		[]byte("broken"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	checks, err = ExtensionChecks(context.Background(), inst.managedRoot, inst.options)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Engine == "claude" && !strings.Contains(check.Problem, "unreadable") {
			t.Errorf("unreadable engine reports clean aspect: %+v", check)
		}
	}
}

// TestConfigureExtensionsHandsCallmeterTheCostLedger: callmeter reads pfm's
// cost ledger only through its own CALLMETER_COST_LEDGER_DIR, so the install
// supplies pfm's ledger directory while callmeter is installed; a value
// claude.env names wins.
func TestConfigureExtensionsHandsCallmeterTheCostLedger(t *testing.T) {
	callmeter := []pfmconfig.ClaudePlugin{{ID: "callmeter"}}
	for _, tc := range []struct {
		name    string
		plugins []pfmconfig.ClaudePlugin
		env     map[string]string // claude.env
		process map[string]string
		want    string
	}{
		{"default ledger", callmeter, nil, nil, "/jail/home/.local/state/pfm/cost-ledger"},
		{"ledger override", callmeter, nil, map[string]string{paths.EnvCostLedgerDir: "/ledger"}, "/ledger"},
		{"claude.env wins", callmeter, map[string]string{"CALLMETER_COST_LEDGER_DIR": "/mine"}, nil, "/mine"},
		{"no callmeter", []pfmconfig.ClaudePlugin{{ID: "buddy"}}, nil, nil, ""},
	} {
		options := Options{Home: "/jail/home", Env: &paths.MapEnv{Values: tc.process}}
		ConfigureExtensions(&options, pfmconfig.Config{Claude: pfmconfig.Claude{Plugins: tc.plugins, Env: tc.env}})
		if got := options.ClaudeEnv["CALLMETER_COST_LEDGER_DIR"]; got != tc.want {
			t.Errorf("%s: CALLMETER_COST_LEDGER_DIR = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestExtensionsComposeWithAlphaDefaultMarketplace(t *testing.T) {
	inst := extensionFixture(t)
	machine := pfmconfig.Config{
		Claude:     pfmconfig.Claude{Plugins: pfmconfig.DefaultClaudePlugins()},
		Extensions: inst.options.Extensions,
	}
	ConfigureExtensions(&inst.options, machine)
	build, err := (ClaudePluginBuild{
		Version:        alphaVersion,
		SourceRepo:     writeMarketplaceClone(t, []string{"engine-sync", "buddy"}, nil),
		Plugins:        inst.options.ClaudePlugins,
		PluginsDefault: inst.options.ClaudePluginsDefault,
	}).WithMarketplacePlugins()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, plugin := range build.Plugins {
		found[plugin.ID] = true
	}
	for _, id := range []string{"engine-sync", "buddy", "ponytail@ponytail"} {
		if !found[id] {
			t.Errorf("alpha defaults dropped %s: %+v", id, build)
		}
	}
}

func TestExtensionChecksDetectRemovedOptions(t *testing.T) {
	inst := extensionFixture(t)
	if err := inst.ensureExtensions(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	ext := inst.options.Extensions["ponytail"]
	ext.Options = nil
	inst.options.Extensions["ponytail"] = ext
	checks, err := ExtensionChecks(context.Background(), inst.managedRoot, inst.options)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Engine == extensionClaude && check.Aspect == extensionOptions &&
			strings.Contains(check.Problem, "awaits restoration") {
			return
		}
	}
	t.Fatalf("removed options rendered healthy: %+v", checks)
}

func TestExtensionChecksRejectMalformedOpenCodePluginArray(t *testing.T) {
	inst := extensionFixture(t)
	writeFixture(t, inst.options.OpenCodeConfigPath, `{"plugin":["@dietrichgebert/ponytail@5.3.2",42]}`)
	checks, err := ExtensionChecks(context.Background(), inst.managedRoot, inst.options)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Engine == extensionOpenCode && !strings.Contains(check.Problem, "entries must be strings") {
			t.Errorf("malformed plugin array rendered healthy: %+v", check)
		}
	}
}
