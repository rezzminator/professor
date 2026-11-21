package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func examplePath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find test source")
	}
	for dir := filepath.Dir(source); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "example.pfm.config.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatal("example.pfm.config.json missing from repository")
	return ""
}

func flattenExample(prefix string, value any, leaves map[string]any) {
	object, ok := value.(map[string]any)
	// claude.settings is one value, the shipped launch defaults, which claudelaunch's
	// TestExampleClaudeSettingsAreTheDefaults holds the example to.
	if !ok || len(object) == 0 || prefix == KeyClaudeSettings {
		leaves[prefix] = value
		return
	}
	for key, child := range object {
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		flattenExample(name, child, leaves)
	}
}

func TestExampleConfigMatchesKeys(t *testing.T) {
	content, err := os.ReadFile(examplePath(t))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(content, &object); err != nil {
		t.Fatal(err)
	}
	actual := map[string]any{}
	flattenExample("", object, actual)
	want := map[string]any{}
	for _, entry := range Keys() {
		// The example omits ask.engine so DefaultEngine can resolve the local roster.
		if entry.Key == "ask.engine" {
			continue
		}
		encoded, err := json.Marshal(entry.Default)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		want[entry.Key] = decoded
	}
	var missing, extra, wrong []string
	for key, expected := range want {
		got, found := actual[key]
		if !found {
			missing = append(missing, key)
		} else if key != KeyClaudeSettings && !reflect.DeepEqual(got, expected) {
			wrong = append(wrong, key)
		}
	}
	for key := range actual {
		if _, found := want[key]; !found {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(wrong)
	if len(missing)+len(extra)+len(wrong) != 0 {
		t.Fatalf("example parity: missing=%v extra=%v wrong-default=%v", missing, extra, wrong)
	}
}

func TestThirdPartyKeyDefault(t *testing.T) {
	for _, key := range Keys() {
		if key.Key == "mcp.thirdParty" {
			if !reflect.DeepEqual(key.Default, map[string]any{}) {
				t.Fatalf("default = %#v, want empty object", key.Default)
			}
			return
		}
	}
	t.Fatal("mcp.thirdParty missing from Keys")
}

// installPing is the machine-wide switch of both install pings: on by default, a file's boolean
// wins with source file, and Marshal writes it only when the file set it, so a default file
// (config init) stays readable by a pfm that predates the key.
func TestLoadInstallPingSwitch(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct {
		content    string
		want       bool
		wantSource Source
		wantJSON   string
	}{
		{`{"version":2}`, true, SourceDefault, ""},
		{`{"version":2,"installPing":true}`, true, SourceFile, `  "installPing": true,`},
		{`{"version":2,"installPing":false}`, false, SourceFile, `  "installPing": false,`},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if err != nil || loaded.InstallPing != tc.want || loaded.Source(KeyInstallPing) != tc.wantSource {
			t.Fatalf("%s installPing=%v source=%q err=%v, want %v %q",
				tc.content, loaded.InstallPing, loaded.Source(KeyInstallPing), err, tc.want, tc.wantSource)
		}
		encoded, err := Marshal(loaded, false)
		if err != nil {
			t.Fatalf("%s marshal: %v", tc.content, err)
		}
		if tc.wantJSON == "" {
			if strings.Contains(string(encoded), `"installPing"`) ||
				!strings.Contains(string(encoded), `"theme": "default"`) {
				t.Fatalf("%s marshals %s; want a theme key and no installPing key", tc.content, encoded)
			}
			continue
		}
		if !strings.Contains(string(encoded), "\n"+tc.wantJSON+"\n") {
			t.Fatalf("%s marshals %s; want the line %q", tc.content, encoded, tc.wantJSON)
		}
	}
	for _, def := range Keys() {
		if def.Key == KeyInstallPing && def.Default != true {
			t.Fatalf("Keys() lists %s default %v, want true", def.Key, def.Default)
		}
	}
}
