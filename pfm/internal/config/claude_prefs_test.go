package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudePluginCheckoutRoot(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct {
		content, want, err string
		source             Source
	}{
		{`{"version":2,"claude":{"pluginCheckoutRoot":"~/src/plugins"}}`, filepath.Join(home, "src", "plugins"), "", SourceFile},
		{`{"version":2,"claude":{"pluginCheckoutRoot":"/opt/plugins/"}}`, "/opt/plugins", "", SourceFile},
		{`{"version":2,"claude":{"pluginCheckoutRoot":null}}`, "", "", SourceDefault},
		{`{"version":2}`, "", "", SourceDefault},
		{`{"version":2,"claude":{"pluginCheckoutRoot":"src"}}`, "", "claude.pluginCheckoutRoot must be absolute or start with ~/ or $HOME/, got \"src\"", ""},
		{
			`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"pluginCheckoutRoot":"/opt/plugins"}}]}`, "",
			"accounts[0].pluginCheckoutRoot is machine-wide: set claude.pluginCheckoutRoot", "",
		},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if tc.err != "" {
			if err == nil || err.Error() != "config "+path+": "+tc.err {
				t.Fatalf("%s error = %v, want %q", tc.content, err, tc.err)
			}
			continue
		}
		if err != nil || loaded.Claude.PluginCheckoutRoot != tc.want ||
			loaded.Source(KeyClaudePluginCheckoutRoot) != tc.source {
			t.Fatalf("%s root=%q source=%q err=%v, want %q %q", tc.content, loaded.Claude.PluginCheckoutRoot,
				loaded.Source(KeyClaudePluginCheckoutRoot), err, tc.want, tc.source)
		}
		encoded, err := Marshal(loaded, false)
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Claude map[string]any `json:"claude"`
		}
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		// An unset root is left out of the file, never written as null or "".
		if got, found := value.Claude["pluginCheckoutRoot"]; (tc.want == "" && found) ||
			(tc.want != "" && got != tc.want) {
			t.Fatalf("%s marshals pluginCheckoutRoot=%v (found %v), want %q", tc.content, got, found, tc.want)
		}
	}
}

// claude.pluginSource is where Professor's plugins come from: release when unset, checkout only
// when the machine file says so, any other value a load error naming both, refused at account
// scope; PluginsFromCheckout reads it.
func TestClaudePluginSource(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct {
		content, want, err string
		checkout           bool
		source             Source
	}{
		{`{"version":2}`, PluginSourceRelease, "", false, SourceDefault},
		{`{"version":2,"claude":{"pluginSource":"release"}}`, PluginSourceRelease, "", false, SourceFile},
		{`{"version":2,"claude":{"pluginSource":"checkout"}}`, PluginSourceCheckout, "", true, SourceFile},
		{`{"version":2,"claude":{"pluginSource":"Checkout"}}`, "", "claude.pluginSource must be \"release\" or \"checkout\", got \"Checkout\"", false, ""},
		{`{"version":2,"claude":{"pluginSource":""}}`, "", "claude.pluginSource must be \"release\" or \"checkout\", got \"\"", false, ""},
		{
			`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"pluginSource":"checkout"}}]}`, "",
			"accounts[0].pluginSource is machine-wide: set claude.pluginSource", false, "",
		},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if tc.err != "" {
			if err == nil || err.Error() != "config "+path+": "+tc.err {
				t.Fatalf("%s error = %v, want %q", tc.content, err, tc.err)
			}
			continue
		}
		if err != nil || loaded.Claude.PluginSource != tc.want || loaded.Claude.PluginsFromCheckout() != tc.checkout ||
			loaded.Source(KeyClaudePluginSource) != tc.source {
			t.Fatalf(
				"%s source=%q checkout=%v from=%q err=%v, want %q %v %q",
				tc.content,
				loaded.Claude.PluginSource,
				loaded.Claude.PluginsFromCheckout(),
				loaded.Source(KeyClaudePluginSource),
				err,
				tc.want,
				tc.checkout,
				tc.source,
			)
		}
	}
}

// claude.costLedger is the machine-wide switch of the per-call cost ledger: on by default, an
// account may not set it, and Marshal writes the value back when the file set it.
func TestClaudeCostLedgerSwitch(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct {
		content, err string
		want         bool
		source       Source
	}{
		{`{"version":2}`, "", true, SourceDefault},
		{`{"version":2,"claude":{"costLedger":false}}`, "", false, SourceFile},
		{`{"version":2,"claude":{"costLedger":true}}`, "", true, SourceFile},
		{
			`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"costLedger":false}}]}`,
			"accounts[0].costLedger is machine-wide: set claude.costLedger", false, "",
		},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if tc.err != "" {
			if err == nil || err.Error() != "config "+path+": "+tc.err {
				t.Fatalf("%s error = %v, want %q", tc.content, err, tc.err)
			}
			continue
		}
		if err != nil || loaded.Claude.CostLedger != tc.want || loaded.Source(KeyClaudeCostLedger) != tc.source {
			t.Fatalf("%s costLedger=%v source=%q err=%v, want %v %q", tc.content, loaded.Claude.CostLedger,
				loaded.Source(KeyClaudeCostLedger), err, tc.want, tc.source)
		}
		encoded, err := Marshal(loaded, false)
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Claude map[string]any `json:"claude"`
		}
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		// The default switch left unset stays out of the file; a value the file set is written.
		got, found := value.Claude["costLedger"]
		if wantFound := tc.source == SourceFile; found != wantFound || (found && got != tc.want) {
			t.Fatalf("%s marshals costLedger=%v (found %v), want %v (found %v)",
				tc.content, got, found, tc.want, wantFound)
		}
	}
	for _, def := range Keys() {
		if def.Key == KeyClaudeCostLedger && def.Default != true {
			t.Fatalf("Keys() lists %s default %v, want true", def.Key, def.Default)
		}
	}
}

// v079ClaudeKeys are the claude.* keys a pfm built at v0.79.0 decodes. It reads the machine file
// strictly (DisallowUnknownFields), so a key outside this set makes it refuse the whole file.
var v079ClaudeKeys = map[string]bool{
	"permissionMode": true, "webSearchesPerSession": true, "autoCompactWindow": true, "tmuxTruecolor": true,
	"cleanupPeriodDays": true, "requireManagedCleanup": true, "binary": true, "theme": true, "cache1h": true,
	"nativeCursor": true, "systemPrompt": true, "compactNudge": true, "maxSubagentSpawnDepth": true,
	"maxConcurrentSubagents": true,
}

// releaseClaudeKeys are the claude.* keys added after v0.79.0.
var releaseClaudeKeys = []string{
	"remoteControl",
	"pluginCheckoutRoot",
	"pluginSource",
	"plugins",
	"costLedger",
	"gptAgents",
}

// pfm config init writes a file a pfm of the last release still decodes: a claude.* key added
// since stays out of the default file (machine block and account block alike) while it is unset or
// at its default, and a value the user set is written, and survives the round trip, even when it
// equals the default.
func TestMarshalClaudeKeysAddedSinceTheLastReleaseAreWrittenOnlyWhenSet(t *testing.T) {
	type claudeBlocks struct {
		Claude   map[string]any `json:"claude"`
		Accounts []struct {
			Claude map[string]any `json:"claude"`
		} `json:"accounts"`
	}
	home := filepath.Join(t.TempDir(), "home")
	t.Run("the default file", func(t *testing.T) {
		defaults := Defaults(home, nil)
		defaults.Accounts = []Account{{
			ID: 1, ConfigDir: filepath.Join(home, ".cc", "1"),
			Claude: &ClaudePrefs{RemoteControl: true},
		}}
		for name, build := range map[string]func() ([]byte, error){
			"MarshalDefault":                  func() ([]byte, error) { return MarshalDefault(home, nil) },
			"an account block at the default": func() ([]byte, error) { return Marshal(defaults, false) },
		} {
			content, err := build()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			var blocks claudeBlocks
			if err := json.Unmarshal(content, &blocks); err != nil {
				t.Fatal(err)
			}
			scopes := []map[string]any{blocks.Claude}
			for _, account := range blocks.Accounts {
				scopes = append(scopes, account.Claude)
			}
			for _, scope := range scopes {
				for key := range scope {
					if !v079ClaudeKeys[key] {
						t.Errorf("%s writes claude.%s, which a v0.79.0 pfm refuses: %s", name, key, content)
					}
				}
			}
		}
	})
	for _, tc := range []struct {
		name, content string
		want          map[string]any
	}{
		{
			"every key set to a non-default",
			`{"version":2,"claude":{"pluginCheckoutRoot":"/opt/plugins","pluginSource":"checkout",` +
				`"plugins":["auto-effort"],"costLedger":false}}`,
			map[string]any{
				"pluginCheckoutRoot": "/opt/plugins", "pluginSource": "checkout",
				"plugins": []any{"auto-effort"}, "costLedger": false,
			},
		},
		{
			"keys the user set to their defaults",
			`{"version":2,"claude":{"costLedger":true,"pluginSource":"release",` +
				`"plugins":["cache-live-control","forward-compaction","auto-effort","tool-visibility-controller"]}}`,
			map[string]any{
				"costLedger": true, "pluginSource": "release",
				"plugins": []any{"cache-live-control", "forward-compaction", "auto-effort", "tool-visibility-controller"},
			},
		},
		{"an explicitly empty list", `{"version":2,"claude":{"plugins":[]}}`, map[string]any{"plugins": []any{}}},
		{"nothing set", `{"version":2}`, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := Marshal(loaded, false)
			if err != nil {
				t.Fatal(err)
			}
			var blocks claudeBlocks
			if err := json.Unmarshal(encoded, &blocks); err != nil {
				t.Fatal(err)
			}
			for _, key := range releaseClaudeKeys {
				got, written := blocks.Claude[key]
				want, wantWritten := tc.want[key]
				if written != wantWritten || !reflect.DeepEqual(got, want) {
					t.Errorf("claude.%s = %#v (written %v), want %#v (written %v)\n%s",
						key, got, written, want, wantWritten, encoded)
				}
			}
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			reloaded, err := Load(path, home, nil)
			if err != nil {
				t.Fatalf("Load of the marshaled file: %v", err)
			}
			if !reflect.DeepEqual(loaded.Claude, reloaded.Claude) {
				t.Errorf("round trip changed the claude block:\n%#v\n%#v", loaded.Claude, reloaded.Claude)
			}
			for _, key := range []string{
				KeyClaudePlugins, KeyClaudePluginCheckoutRoot, KeyClaudePluginSource, KeyClaudeCostLedger,
			} {
				if loaded.Source(key) != reloaded.Source(key) {
					t.Errorf(
						"round trip moved the source of %s: %s -> %s",
						key,
						loaded.Source(key),
						reloaded.Source(key),
					)
				}
			}
		})
	}
}

func TestGptAgentsMachineWideOptInAndDefaultOmission(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, FileName)
	defaults := Defaults(home, nil)
	body, err := Marshal(defaults, false)
	if err != nil || strings.Contains(string(body), `"gptAgents"`) {
		t.Fatalf("default gpt key emitted: %s %v", body, err)
	}
	for _, enabled := range []string{"true", "false"} {
		if err := os.WriteFile(path, []byte(`{"version":2,"claude":{"gptAgents":`+enabled+`}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if err != nil {
			t.Fatalf("registered opt-in refused: %v", err)
		}
		if loaded.Source("claude.gptAgents") != SourceFile {
			t.Fatal("opt-in lost provenance")
		}
		body, err := Marshal(loaded, false)
		if err != nil || !strings.Contains(string(body), `"gptAgents": `+enabled) {
			t.Fatalf("explicit key lost: %s %v", body, err)
		}
	}
	if err := os.WriteFile(
		path,
		[]byte(`{"version":2,"accounts":[{"id":1,"configDir":"~/account","claude":{"gptAgents":true}}]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, home, nil); err == nil || !strings.Contains(err.Error(), "machine-wide") {
		t.Fatalf("account opt-in refusal=%v", err)
	}
}

// claude.permissionMode takes bypass, prompted or auto; anything else names
// every accepted value.
func TestClaudePermissionModeValues(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, mode := range []string{PermissionBypass, PermissionPrompt, "auto"} {
		content := `{"version":2,"claude":{"permissionMode":"` + mode + `"}}`
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if err != nil || loaded.EffectiveClaude(0).PermissionMode != mode {
			t.Fatalf("permissionMode %q: err=%v loaded=%+v", mode, err, loaded.Claude)
		}
	}
	if err := os.WriteFile(path, []byte(`{"version":2,"claude":{"permissionMode":"yolo"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "config " + path + `: claude.permissionMode must be "bypass", "prompted" or "auto", got "yolo"`
	if _, err := Load(path, home, nil); err == nil || err.Error() != want {
		t.Fatalf("junk permissionMode error = %v, want %s", err, want)
	}
}
