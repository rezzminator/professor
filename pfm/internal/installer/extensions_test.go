package installer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func extensionFixture(t *testing.T) *engine {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "seat")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"unrelated":"keep","pluginConfigs":{"ponytail@ponytail":{"options":{"foreign":"keep"}}}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	oc := filepath.Join(home, "opencode.jsonc")
	if err := os.WriteFile(
		oc,
		[]byte("{\n // keep comment\n \"plugin\":[\"opencode-goal-plugin\"],\n \"theme\":\"keep\"\n}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return &engine{
		apply:       true,
		managedRoot: filepath.Join(home, "managed"),
		options: Options{
			Home:               home,
			PrimaryConfigDir:   dir,
			ConfigDir:          dir,
			OpenCodeConfigPath: oc,
			CodexHomes:         []string{},
			Stdout:             io.Discard,
			Extensions: map[string]pfmconfig.Extension{
				"ponytail": {
					Enabled: true,
					Engines: []string{"claude", "opencode"},
					Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "^worker$"},
					Options: map[string]any{"ledger": false, "sendNames": false, "webhookUrl": ""},
				},
			},
		},
	}
}

func TestExtensionsOnOffDroppedUninstall(t *testing.T) {
	for _, transition := range []string{"off", "engine dropped", "extension dropped", "uninstall"} {
		t.Run(transition, func(t *testing.T) {
			inst := extensionFixture(t)
			ctx := context.Background()
			if err := inst.ensureExtensions(ctx, false); err != nil {
				t.Fatal(err)
			}
			_, claude, err := readExtensionDocument(
				filepath.Join(inst.options.PrimaryConfigDir, "settings.json"),
				"claude",
			)
			if err != nil {
				t.Fatal(err)
			}
			if value, _, _ := extensionLookup(claude, []string{"enabledPlugins", "ponytail@ponytail"}); value != true {
				t.Fatalf("ON not enabled: %v", claude)
			}
			if value, _, _ := extensionLookup(
				claude,
				[]string{"pluginConfigs", "ponytail@ponytail", "options", "ledger"},
			); value != false {
				t.Fatalf("option not set: %v", claude)
			}
			_, oc, err := readExtensionDocument(inst.options.OpenCodeConfigPath, "opencode")
			if err != nil {
				t.Fatal(err)
			}
			if !extensionPackagePresent(oc, "@dietrichgebert/ponytail@5.3.2") ||
				!extensionPackagePresent(oc, "opencode-goal-plugin") {
				t.Fatalf("ON plugin array %v", oc)
			}
			// No real engine installed these fixtures: preserve the intentionally absent cache on uninstall.
			ledger, err := readExtensionOwnership(inst.managedRoot)
			if err != nil {
				t.Fatal(err)
			}
			for key, record := range ledger.Records {
				record.InstalledByPFM = false
				ledger.Records[key] = record
			}
			raw, err := json.Marshal(ledger)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(inst.managedRoot, extensionOwnershipName), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			ext := inst.options.Extensions["ponytail"]
			switch transition {
			case "off":
				ext.Enabled = false
			case "engine dropped":
				ext.Engines = []string{}
			case "extension dropped":
				inst.options.Extensions = nil
			}
			if transition != "extension dropped" {
				inst.options.Extensions["ponytail"] = ext
			}
			if err := inst.ensureExtensions(ctx, transition == "uninstall"); err != nil {
				t.Fatal(err)
			}
			_, claude, err = readExtensionDocument(
				filepath.Join(inst.options.PrimaryConfigDir, "settings.json"),
				"claude",
			)
			if err != nil {
				t.Fatal(err)
			}
			enabled, has, _ := extensionLookup(claude, []string{"enabledPlugins", "ponytail@ponytail"})
			if transition == "uninstall" {
				if has {
					t.Fatalf("owned enabled key survived uninstall: %v", claude)
				}
			} else if enabled != false {
				t.Fatalf("OFF not disabled: %v", claude)
			}
			if value, _, _ := extensionLookup(
				claude,
				[]string{"pluginConfigs", "ponytail@ponytail", "options", "foreign"},
			); value != "keep" {
				t.Fatalf("foreign option lost: %v", claude)
			}
			raw, oc, err = readExtensionDocument(inst.options.OpenCodeConfigPath, "opencode")
			if err != nil {
				t.Fatal(err)
			}
			if extensionPackagePresent(oc, "@dietrichgebert/ponytail@5.3.2") ||
				!extensionPackagePresent(oc, "opencode-goal-plugin") ||
				!strings.Contains(string(raw), "// keep comment") {
				t.Fatalf("OFF changed foreign entries: %s", raw)
			}
			if transition == "uninstall" {
				if _, err := os.Stat(filepath.Join(inst.managedRoot, extensionOwnershipName)); !os.IsNotExist(err) {
					t.Fatalf("ledger survived uninstall: %v", err)
				}
			}
		})
	}
}

func TestExtensionsPreviewDoesNotWriteAndUnreadableIsError(t *testing.T) {
	inst := extensionFixture(t)
	inst.apply = false
	if err := inst.ensureExtensions(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inst.managedRoot, extensionOwnershipName)); !os.IsNotExist(err) {
		t.Fatalf("preview wrote ledger: %v", err)
	}
	path := filepath.Join(inst.options.PrimaryConfigDir, "settings.json")
	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inst.ensureExtensions(
		context.Background(),
		false,
	); err == nil ||
		!strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("unreadable state rendered clean: %v", err)
	}
}

func TestExtensionsPrimarySeatSymlinkAndHandEditSurvives(t *testing.T) {
	inst := extensionFixture(t)
	path := filepath.Join(inst.options.PrimaryConfigDir, "settings.json")
	target := filepath.Join(inst.options.Home, "shared.json")
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := inst.ensureExtensions(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := EditSettings(path, func(document map[string]any) error {
		return extensionSet(
			document,
			[]string{"pluginConfigs", "ponytail@ponytail", "options", "ledger"},
			"hand-edited",
			false,
		)
	}); err != nil {
		t.Fatal(err)
	}
	ledger, err := readExtensionOwnership(inst.managedRoot)
	if err != nil {
		t.Fatal(err)
	}
	for key, record := range ledger.Records {
		record.InstalledByPFM = false
		ledger.Records[key] = record
	}
	raw, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.managedRoot, extensionOwnershipName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inst.ensureExtensions(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(path); err != nil {
		t.Fatalf("settings symlink replaced: %v", err)
	}
	_, document, err := readExtensionDocument(path, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if value, _, _ := extensionLookup(
		document,
		[]string{"pluginConfigs", "ponytail@ponytail", "options", "ledger"},
	); value != "hand-edited" {
		t.Fatalf("hand edit lost: %v", document)
	}
}

func TestExtensionInitialOffDisablesPreexistingAndUninstallRestores(t *testing.T) {
	inst := extensionFixture(t)
	ext := inst.options.Extensions["ponytail"]
	ext.Enabled = false
	ext.Engines = []string{"claude", "codex", "opencode"}
	inst.options.Extensions["ponytail"] = ext
	codexHome := filepath.Join(inst.options.Home, "codex")
	inst.options.CodexHomes = []string{codexHome}
	claudePath := filepath.Join(inst.options.PrimaryConfigDir, "settings.json")
	writeFixture(t, claudePath, `{"enabledPlugins":{"ponytail@ponytail":true}}`)
	writeFixture(t, filepath.Join(codexHome, "config.toml"), "[plugins.\"ponytail@ponytail\"]\nenabled=true\n")
	writeFixture(t, inst.options.OpenCodeConfigPath, `{"plugin":["foreign","@dietrichgebert/ponytail@5.3.2"]}`)
	cache := filepath.Join(codexHome, "plugins", "cache", "ponytail", "ponytail", "5.3.2")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	checks, err := ExtensionChecks(context.Background(), inst.managedRoot, inst.options)
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range ext.Engines {
		found := false
		for _, check := range checks {
			if check.Engine == engine && check.Aspect == extensionEnabled && check.Problem != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("initial OFF drift hidden for %s: %+v", engine, checks)
		}
	}
	if err := inst.ensureExtensions(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	checks, err = ExtensionChecks(context.Background(), inst.managedRoot, inst.options)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Problem != "" {
			t.Errorf("OFF did not converge: %+v", check)
		}
	}
	if err := inst.ensureExtensions(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	for _, target := range extensionTargets(inst.options, extensionOwnership{}) {
		_, doc, err := readExtensionDocument(target.path, target.engine)
		if err != nil {
			t.Fatal(err)
		}
		if target.engine == extensionOpenCode {
			if !extensionPackagePresent(doc, target.fact.Package) || !extensionPackagePresent(doc, "foreign") {
				t.Errorf("original OpenCode membership not restored: %v", doc)
			}
			continue
		}
		path := []string{extensionEnabledPlugins, target.fact.ID}
		if target.engine == extensionCodex {
			path = []string{extensionPlugins, target.fact.ID, extensionEnabled}
		}
		if value, _, err := extensionLookup(doc, path); err != nil || value != true {
			t.Errorf("original %s enablement not restored: %v err=%v", target.engine, doc, err)
		}
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("OFF/uninstall removed preexisting cache: %v", err)
	}
}
