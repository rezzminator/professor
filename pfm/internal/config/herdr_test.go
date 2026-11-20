package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestHerdrKeysLoadAndSummary holds the decoded herdr.keys and its
// `pfm config show` line: a string, a list and null per action, a full command
// entry and one holding only its key.
func TestHerdrKeysLoadAndSummary(t *testing.T) {
	for _, tc := range []struct {
		name, json, summary string
		want                *HerdrKeys
	}{
		{name: "unset", json: `{"version":2}`, summary: "config herdr.keys=none (default)\n"},
		{name: "null", json: `{"version":2,"herdr":{"keys":null}}`, summary: "config herdr.keys=none (default)\n"},
		{
			name: "every shape",
			json: `{"version":2,"herdr":{"keys":{"previous_workspace":"cmd+shift+up","next_workspace":["cmd+shift+down"],` +
				`"close_pane":null,"command":[{"key":"prefix+a"},{"key":"ctrl+shift+` + "`" + `","type":"shell","description":"d","command":"c"}]}}}`,
			want: &HerdrKeys{
				Actions: map[string]*HerdrKeyBinding{
					"previous_workspace": {Keys: []string{"cmd+shift+up"}},
					"next_workspace":     {Keys: []string{"cmd+shift+down"}, List: true},
					"close_pane":         nil,
				},
				Commands: []HerdrKeyCommand{
					{Key: "prefix+a", Remove: true},
					{Key: "ctrl+shift+`", Type: "shell", Description: "d", Command: "c"},
				},
			},
			summary: `config herdr.keys={"close_pane":null,"command":[{"key":"prefix+a"},` +
				`{"command":"c","description":"d","key":"ctrl+shift+` + "`" + `","type":"shell"}],` +
				`"next_workspace":["cmd+shift+down"],"previous_workspace":"cmd+shift+up"} (file)` + "\n",
		},
		{
			name: "empty object", json: `{"version":2,"herdr":{"keys":{}}}`,
			want: &HerdrKeys{Actions: map[string]*HerdrKeyBinding{}}, summary: "config herdr.keys={} (file)\n",
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
		if !reflect.DeepEqual(loaded.Herdr.Keys, tc.want) {
			t.Errorf(
				"%s: herdr.keys = %s, want %s",
				tc.name,
				fmt.Sprintf("%+v", loaded.Herdr.Keys),
				fmt.Sprintf("%+v", tc.want),
			)
		}
		if summary := loaded.HerdrSummary(); summary != tc.summary {
			t.Errorf("%s: HerdrSummary = %q, want %q", tc.name, summary, tc.summary)
		}
	}
}
