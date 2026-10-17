package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestClaudeEnvKey pins claude.env: a machine-wide map of upper-snake
// variable names to string values, empty when unset, refused at account scope,
// and written back only when the file set it (a v0.79.0 pfm refuses the key).
func TestClaudeEnvKey(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct {
		name, content string
		want          map[string]string
		source        Source
		err           string
	}{
		{
			name: "absent key is unset", content: `{"version":2}`,
			want: nil, source: SourceDefault,
		},
		{
			name: "null is absent", content: `{"version":2,"claude":{"env":null}}`,
			want: nil, source: SourceDefault,
		},
		{
			name:    "upper-snake names with string values",
			content: `{"version":2,"claude":{"env":{"SUB_AGENT_COMPACT_FORWARD_HANDOFF_TOOL":"1","BUDDY_MODEL":"opus","X9":""}}}`,
			want: map[string]string{
				"SUB_AGENT_COMPACT_FORWARD_HANDOFF_TOOL": "1", "BUDDY_MODEL": "opus", "X9": "",
			},
			source: SourceFile,
		},
		{
			name: "an empty object", content: `{"version":2,"claude":{"env":{}}}`,
			want: map[string]string{}, source: SourceFile,
		},
		{
			name: "a lower-case name", content: `{"version":2,"claude":{"env":{"buddy_model":"opus"}}}`,
			err: `claude.env name "buddy_model" must be upper snake: A-Z, 0-9 and _, starting with a letter`,
		},
		{
			name: "a name starting with a digit", content: `{"version":2,"claude":{"env":{"9LIVES":"1"}}}`,
			err: `claude.env name "9LIVES" must be upper snake: A-Z, 0-9 and _, starting with a letter`,
		},
		{
			name: "a name with a dash", content: `{"version":2,"claude":{"env":{"SUB-AGENT":"1"}}}`,
			err: `claude.env name "SUB-AGENT" must be upper snake: A-Z, 0-9 and _, starting with a letter`,
		},
		{
			name: "a null value", content: `{"version":2,"claude":{"env":{"BUDDY_MODEL":null}}}`,
			err: `claude.env.BUDDY_MODEL must be a string, got null`,
		},
		{
			name: "a number value", content: `{"version":2,"claude":{"env":{"BUDDY_CHAT_TURNS_TO_READ":4}}}`,
			err: `claude.env.BUDDY_CHAT_TURNS_TO_READ must be a string, got 4`,
		},
		{
			name: "a boolean value", content: `{"version":2,"claude":{"env":{"BUDDY_SAVE_ROUNDS":true}}}`,
			err: `claude.env.BUDDY_SAVE_ROUNDS must be a string, got true`,
		},
		{
			name:    "a variable the launch sets from claude.cache1h",
			content: `{"version":2,"claude":{"env":{"CACHE_LIVE_CONTROL_MAIN_TTL":"1h"}}}`,
			err:     `claude.env.CACHE_LIVE_CONTROL_MAIN_TTL is set on every launch from claude.cache1h: set that key instead`,
		},
		{
			name:    "a variable the launch discards as inherited state",
			content: `{"version":2,"claude":{"env":{"CACHE_LIVE_CONTROL_AGENTS_TTL":"1h"}}}`,
			err:     `claude.env.CACHE_LIVE_CONTROL_AGENTS_TTL is discarded on every launch as inherited state: set CACHE_LIVE_CONTROL_SUBAGENT_TTL instead`,
		},
		{
			name:    "a home pfm resolves from its own environment",
			content: `{"version":2,"claude":{"env":{"CALLMETER_HOME":"/x"}}}`,
			err:     `claude.env.CALLMETER_HOME is read by pfm itself, which builds callmeter's binary under it: export it where pfm runs instead`,
		},
		{
			name:    "engine-sync's home",
			content: `{"version":2,"claude":{"env":{"ENGINE_SYNC_HOME":"/x"}}}`,
			err:     `claude.env.ENGINE_SYNC_HOME is read by pfm itself, which runs engine-sync from it: export it where pfm runs instead`,
		},
		{
			name: "not an object", content: `{"version":2,"claude":{"env":["A=1"]}}`,
			err: `claude.env must be an object of variable names to string values, got ["A=1"]`,
		},
		{
			name: "an account may not set it",
			content: `{"version":2,"accounts":[{"id":1,"configDir":"` + filepath.Join(home, ".cc", "1") +
				`","claude":{"env":{"BUDDY_MODEL":"opus"}}}]}`,
			err: `accounts[0].env is machine-wide: set claude.env`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path, home, nil)
			if tc.err != "" {
				if err == nil || err.Error() != "config "+path+": "+tc.err {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !reflect.DeepEqual(loaded.Claude.Env, tc.want) || loaded.Source(KeyClaudeEnv) != tc.source {
				t.Fatalf(
					"env = %#v (%s), want %#v (%s)",
					loaded.Claude.Env,
					loaded.Source(KeyClaudeEnv),
					tc.want,
					tc.source,
				)
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
			got, found := value.Claude["env"]
			if tc.source == SourceDefault {
				if found {
					t.Fatalf("Marshal wrote the unset env map: %#v", got)
				}
				return
			}
			want := map[string]any{}
			for key, value := range tc.want {
				want[key] = value
			}
			if !found || !reflect.DeepEqual(got, any(want)) {
				t.Fatalf("Marshal env = %#v (found %v), want %#v", got, found, want)
			}
		})
	}
}
