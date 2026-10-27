package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/mockengine"
)

func TestExtensionCLIChangesCaptureOnlyManagedNamespaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	raw := "[plugins.\"ponytail@ponytail\"]\nenabled=true\n[marketplaces.ponytail]\nsource='fixture'\n[hooks.state.\"ponytail@ponytail:hook\"]\nenabled=true\ntrusted_hash='new'\n[hooks.state.\"foreign@foreign:hook\"]\nenabled=true\ntrusted_hash='foreign'\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	record := extensionRecord{Values: map[string]extensionValue{}}
	target := extensionTarget{name: "ponytail", engine: "codex", path: path}
	target.fact.ID = "ponytail@ponytail"
	target.fact.Marketplace = "ponytail"
	if err := captureExtensionCLIChanges(target, map[string]any{}, &record); err != nil {
		t.Fatal(err)
	}
	if len(record.Values) != 4 {
		t.Fatalf("captured %d fields, want 4: %v", len(record.Values), record.Values)
	}
	for _, value := range record.Values {
		for _, part := range value.Path {
			if part == "foreign@foreign:hook" {
				t.Fatalf("claimed foreign trust: %v", record)
			}
		}
	}
}

func TestExtensionLifecycleWithMockEngines(t *testing.T) {
	inst := extensionFixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(inst.options.Home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex"} {
		if err := os.Symlink(binary, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MOCK_ENGINE_HELPER", "1")
	scenario := filepath.Join(inst.options.Home, "scenario.json")
	if err := os.WriteFile(scenario, []byte(`{"rate_limits":{"primary":{"usedPercent":0}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(mockengine.EnvScenario, scenario)
	t.Setenv(mockengine.EnvEngine, "")
	inst.options.ClaudeBinary = filepath.Join(bin, "claude")
	inst.options.CodexBinary = filepath.Join(bin, "codex")
	inst.options.CodexHomes = []string{
		filepath.Join(inst.options.Home, "codex-one"),
		filepath.Join(inst.options.Home, "codex-two"),
	}
	for _, home := range inst.options.CodexHomes {
		if err := os.MkdirAll(home, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	machine := pfmconfig.Config{Extensions: inst.options.Extensions}
	ext := machine.Extensions["ponytail"]
	ext.Engines = []string{"claude", "codex", "opencode"}
	machine.Extensions["ponytail"] = ext
	ConfigureExtensions(&inst.options, machine)
	ctx := context.Background()
	converge := func() {
		t.Helper()
		if err := inst.ensureExtensions(ctx, false); err != nil {
			t.Fatal(err)
		}
		if err := inst.ensureClaudePlugins(ctx); err != nil {
			t.Fatal(err)
		}
		if err := inst.recordClaudeExtensionMarketplaces(); err != nil {
			t.Fatal(err)
		}
		if err := inst.ensureClaudeEnv(false); err != nil {
			t.Fatal(err)
		}
	}
	converge()
	checks, err := ExtensionChecks(ctx, inst.managedRoot, inst.options)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Problem != "" {
			t.Errorf("ON drift: %+v", check)
		}
	}
	for _, home := range inst.options.CodexHomes {
		raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil ||
			!strings.Contains(string(raw), "ponytail@ponytail:hooks/claude-codex-hooks.json:subagent_start:0:0") {
			t.Fatalf("plugin hook trust absent in %s: %s err=%v", home, raw, err)
		}
	}
	ext.Enabled = false
	machine.Extensions["ponytail"] = ext
	ConfigureExtensions(&inst.options, machine)
	converge()
	for _, home := range inst.options.CodexHomes {
		if _, err := os.Stat(filepath.Join(home, "plugins", "cache", "ponytail", "ponytail", "5.3.2")); err != nil {
			t.Fatalf("OFF removed cached plugin: %v", err)
		}
	}
	ext.Enabled = true
	machine.Extensions["ponytail"] = ext
	ConfigureExtensions(&inst.options, machine)
	converge()
	// A refreshed plugin changes the native hook hashes; uninstall must still
	// recognize the trust values written by the subsequent ON install as ours.
	for _, home := range inst.options.CodexHomes {
		path := filepath.Join(
			home,
			"plugins",
			"cache",
			"ponytail",
			"ponytail",
			"5.3.2",
			"hooks",
			"claude-codex-hooks.json",
		)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			path,
			[]byte(strings.ReplaceAll(string(raw), "printf fixture", "printf refreshed")),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	converge()
	if err := inst.ensureExtensions(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := inst.ensureClaudeEnv(true); err != nil {
		t.Fatal(err)
	}
	for _, home := range inst.options.CodexHomes {
		raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "ponytail") || strings.Contains(string(raw), "trusted_hash") {
			t.Fatalf("uninstall left owned config: %s", raw)
		}
		if _, err := os.Stat(
			filepath.Join(home, "plugins", "cache", "ponytail", "ponytail", "5.3.2"),
		); !os.IsNotExist(
			err,
		) {
			t.Fatalf("owned cache survived uninstall: %v", err)
		}
	}
}

func TestClaudeExtensionUninstallPreservesHandEditedEnablement(t *testing.T) {
	for _, handEdit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "hand-edited"}[handEdit], func(t *testing.T) {
			inst := extensionFixture(t)
			ext := inst.options.Extensions["ponytail"]
			ext.Engines = []string{"claude"}
			inst.options.Extensions["ponytail"] = ext
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(inst.options.Home, "claude")
			if err := os.Symlink(binary, bin); err != nil {
				t.Fatal(err)
			}
			inst.options.ClaudeBinary = bin
			t.Setenv("MOCK_ENGINE_HELPER", "1")
			t.Setenv(mockengine.EnvEngine, "")
			t.Setenv(mockengine.EnvScenario, "")
			ConfigureExtensions(&inst.options, pfmconfig.Config{Extensions: inst.options.Extensions})
			settings := filepath.Join(inst.options.PrimaryConfigDir, "settings.json")
			raw, doc, err := readExtensionDocument(settings, "claude")
			if err != nil {
				t.Fatal(err)
			}
			prior := handEdit
			if err := extensionSet(doc, []string{"enabledPlugins", "ponytail@ponytail"}, prior, false); err != nil {
				t.Fatal(err)
			}
			wanted, err := encodeSettingsDocument(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := inst.writeExtensionDocument(settings, raw, wanted); err != nil {
				t.Fatal(err)
			}
			if err := inst.ensureExtensions(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			if handEdit {
				raw, doc, err = readExtensionDocument(settings, "claude")
				if err != nil {
					t.Fatal(err)
				}
				if err := extensionSet(
					doc,
					[]string{"enabledPlugins", "ponytail@ponytail"},
					false,
					false,
				); err != nil {
					t.Fatal(err)
				}
				wanted, err = encodeSettingsDocument(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := inst.writeExtensionDocument(settings, raw, wanted); err != nil {
					t.Fatal(err)
				}
			}
			if err := inst.ensureExtensions(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			_, doc, err = readExtensionDocument(settings, "claude")
			if err != nil {
				t.Fatal(err)
			}
			got, has, err := extensionLookup(doc, []string{"enabledPlugins", "ponytail@ponytail"})
			if err != nil {
				t.Fatal(err)
			}
			want := false
			if !has || got != want {
				t.Fatalf("uninstall enablement=%v present=%t want %v", got, has, want)
			}
		})
	}
}
