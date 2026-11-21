package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestHerdrSettingsLoadSummaryAndMarshal holds the decoded herdr.settings, its
// `pfm config show` line and the herdr block Marshal writes back: a top-level
// key, a table, a null, an integer kept an integer and an array.
func TestHerdrSettingsLoadSummaryAndMarshal(t *testing.T) {
	for _, tc := range []struct {
		name, json, summary, marshal string
		want                         map[string]any
	}{
		{
			name: "null", json: `{"version":2,"herdr":{"settings":null}}`,
			summary: "config herdr.settings=none (default)\n", marshal: "null",
		},
		{
			name: "every shape",
			json: `{"version":2,"herdr":{"settings":{"onboarding":false,"ui":{"status_indicators":null,` +
				`"sidebar_width":30},"terminal":{"args":["-l",2]}}}}`,
			want: map[string]any{
				"onboarding": false,
				"ui":         map[string]any{"status_indicators": nil, "sidebar_width": json.Number("30")},
				"terminal":   map[string]any{"args": []any{"-l", json.Number("2")}},
			},
			summary: `config herdr.settings={"onboarding":false,"terminal":{"args":["-l",2]},` +
				`"ui":{"sidebar_width":30,"status_indicators":null}} (file)` + "\n",
			marshal: `{"settings":{"onboarding":false,"terminal":{"args":["-l",2]},` +
				`"ui":{"sidebar_width":30,"status_indicators":null}}}`,
		},
		{
			name: "empty object", json: `{"version":2,"herdr":{"settings":{}}}`, want: map[string]any{},
			summary: "config herdr.settings={} (file)\n", marshal: `{"settings":{}}`,
		},
	} {
		path := filepath.Join(t.TempDir(), "pfm.config.json")
		if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, filepath.Join(t.TempDir(), "home"), nil)
		if err != nil {
			t.Fatalf("%s: Load: %v", tc.name, err)
		}
		if !reflect.DeepEqual(loaded.Herdr.Settings, tc.want) {
			t.Errorf("%s: herdr.settings = %#v, want %#v", tc.name, loaded.Herdr.Settings, tc.want)
		}
		const keys = "config herdr.keys=none (default)\n"
		if summary := loaded.HerdrSummary(); summary != keys+tc.summary {
			t.Errorf("%s: HerdrSummary = %q, want %q", tc.name, summary, keys+tc.summary)
		}
		body, err := Marshal(loaded, false)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", tc.name, err)
		}
		var written map[string]json.RawMessage
		if err := json.Unmarshal(body, &written); err != nil {
			t.Fatalf("%s: parse marshalled %s: %v", tc.name, body, err)
		}
		herdr := string(written[KeyHerdr])
		if herdr == "" {
			herdr = "null"
		}
		if got := strings.Join(strings.Fields(herdr), ""); got != tc.marshal {
			t.Errorf("%s: marshalled herdr = %s, want %s", tc.name, got, tc.marshal)
		}
	}
}

// TestHerdrSettingsRefusals: load refuses each shape herdr.settings cannot
// hold, naming its path, and an account's herdr block.
func TestHerdrSettingsRefusals(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, tc := range []struct{ name, json, want string }{
		{
			"not an object", `{"version":2,"herdr":{"settings":[]}}`,
			"config {path}: herdr.settings must be an object of Herdr config.toml keys",
		},
		{
			"naming the keymap", `{"version":2,"herdr":{"settings":{"keys":{}}}}`,
			"config {path}: herdr.settings.keys is Herdr's keymap: set it in herdr.keys",
		},
		{
			"an array of tables", `{"version":2,"herdr":{"settings":{"ui":{"rows":["a",{"b":1}]}}}}`,
			"config {path}: herdr.settings.ui.rows[1] must be a string, number, boolean or array",
		},
		{
			"an array holding null", `{"version":2,"herdr":{"settings":{"rows":[null]}}}`,
			"config {path}: herdr.settings.rows[0] must be a string, number, boolean or array",
		},
		{
			"a nested array holding a table", `{"version":2,"herdr":{"settings":{"rows":[["a"],[{"b":1}]]}}}`,
			"config {path}: herdr.settings.rows[1][0] must be a string, number, boolean or array",
		},
		{
			"an integer past 64 bits", `{"version":2,"herdr":{"settings":{"ui":{"width":[99999999999999999999]}}}}`,
			"config {path}: herdr.settings.ui.width[0] must be an integer TOML can hold (64-bit), not 99999999999999999999",
		},
		{
			"at account scope", `{"version":2,"accounts":[{"id":1,"configDir":"~/one","herdr":{"settings":{}}}]}`,
			"parse config {path}: json: unknown field \"herdr\"",
		},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path, home, nil)
		if got := strings.ReplaceAll(fmt.Sprint(err), path, "{path}"); got != tc.want {
			t.Errorf("%s: Load error = %q, want %q", tc.name, got, tc.want)
		}
	}
}
