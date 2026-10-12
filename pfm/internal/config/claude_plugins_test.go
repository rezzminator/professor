package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestClaudePluginsKey pins claude.plugins: the list's entry shapes (a bare
// name, a full id, an {"id","source"} object), the default when the key is
// absent, and the entry-by-entry validation naming the index and the reason.
func TestClaudePluginsKey(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct {
		name, content string
		want          []ClaudePlugin
		wantMarshal   []any
		source        Source
		err           string
	}{
		{
			name: "absent key keeps the four of today", content: `{"version":2}`,
			want: []ClaudePlugin{
				{ID: "cache-live-control"},
				{ID: "sub-agent-compact"},
				{ID: "agent-effort"},
				{ID: "tool-visibility-controller"},
			},
			source: SourceDefault,
		},
		{
			name: "null is absent", content: `{"version":2,"claude":{"plugins":null}}`,
			want:   DefaultClaudePlugins(),
			source: SourceDefault,
		},
		{
			name: "an empty list names no plugin", content: `{"version":2,"claude":{"plugins":[]}}`,
			want: []ClaudePlugin{}, wantMarshal: []any{}, source: SourceFile,
		},
		{
			name: "bare names, full ids and a source object",
			content: `{"version":2,"claude":{"plugins":["callmeter","clangd-lsp@claude-plugins-official",` +
				`{"id":"typesafe@typesafe-ai","source":"typesafe-ai/skills"},{"id":"swift-lsp@claude-plugins-official"}]}}`,
			want: []ClaudePlugin{
				{ID: "callmeter"},
				{ID: "clangd-lsp@claude-plugins-official"},
				{ID: "typesafe@typesafe-ai", Source: "typesafe-ai/skills"},
				{ID: "swift-lsp@claude-plugins-official"},
			},
			wantMarshal: []any{
				"callmeter", "clangd-lsp@claude-plugins-official",
				map[string]any{"id": "typesafe@typesafe-ai", "source": "typesafe-ai/skills"},
				"swift-lsp@claude-plugins-official",
			},
			source: SourceFile,
		},
		{
			name: "an empty id", content: `{"version":2,"claude":{"plugins":["ok",""]}}`,
			err: `claude.plugins[1] id must be a plugin name or name@marketplace, got ""`,
		},
		{
			name: "an id with a space", content: `{"version":2,"claude":{"plugins":["bad name"]}}`,
			err: `claude.plugins[0] id must be a plugin name or name@marketplace, got "bad name"`,
		},
		{
			name: "an id with an empty marketplace", content: `{"version":2,"claude":{"plugins":["a","b","tool@"]}}`,
			err: `claude.plugins[2] id must be a plugin name or name@marketplace, got "tool@"`,
		},
		{
			name: "an id with two marketplaces", content: `{"version":2,"claude":{"plugins":["a@b@c"]}}`,
			err: `claude.plugins[0] id must be a plugin name or name@marketplace, got "a@b@c"`,
		},
		{
			name: "an id that climbs out of its directory", content: `{"version":2,"claude":{"plugins":["../x"]}}`,
			err: `claude.plugins[0] id must be a plugin name or name@marketplace, got "../x"`,
		},
		{
			name: "an entry that is neither", content: `{"version":2,"claude":{"plugins":["a",5]}}`,
			err: `claude.plugins[1] must be a plugin id string or an {"id","source"} object, got 5`,
		},
		{
			name: "an object with an unknown field", content: `{"version":2,"claude":{"plugins":[{"id":"a@b","extra":1}]}}`,
			err: `claude.plugins[0] is not an {"id","source"} object: json: unknown field "extra"`,
		},
		{
			name: "an object without an id", content: `{"version":2,"claude":{"plugins":[{"source":"o/r"}]}}`,
			err: `claude.plugins[0] id must be a plugin name or name@marketplace, got ""`,
		},
		{
			name: "a source on a bare name", content: `{"version":2,"claude":{"plugins":[{"id":"callmeter","source":"o/r"}]}}`,
			err: `claude.plugins[0] source needs a name@marketplace id: a bare name resolves to its own GitHub repository or checkout`,
		},
		{
			name: "an empty source", content: `{"version":2,"claude":{"plugins":[{"id":"a@b","source":""}]}}`,
			err: `claude.plugins[0] source must be one word that does not start with "-", got ""`,
		},
		{
			name: "a source that reads as a flag", content: `{"version":2,"claude":{"plugins":[{"id":"a@b","source":"--scope"}]}}`,
			err: `claude.plugins[0] source must be one word that does not start with "-", got "--scope"`,
		},
		{
			name: "a source with a space", content: `{"version":2,"claude":{"plugins":[{"id":"a@b","source":"o/r x"}]}}`,
			err: `claude.plugins[0] source must be one word that does not start with "-", got "o/r x"`,
		},
		{
			name: "the same id twice", content: `{"version":2,"claude":{"plugins":["a@b","c",{"id":"a@b","source":"o/r"}]}}`,
			err: `claude.plugins[2] repeats claude.plugins[0]: a@b`,
		},
		{
			name: "one marketplace from two sources",
			content: `{"version":2,"claude":{"plugins":[{"id":"a@m","source":"o/one"},"c@m",` +
				`{"id":"b@m","source":"o/two"}]}}`,
			err: `claude.plugins[2] marketplace m has source o/two, but claude.plugins[0] gives it o/one: ` +
				`one marketplace has one source`,
		},
		{
			name: "one marketplace with its source once",
			content: `{"version":2,"claude":{"plugins":[{"id":"a@m","source":"o/one"},"c@m",` +
				`{"id":"b@m","source":"o/one"}]}}`,
			want: []ClaudePlugin{{ID: "a@m", Source: "o/one"}, {ID: "c@m"}, {ID: "b@m", Source: "o/one"}},
			wantMarshal: []any{
				map[string]any{"id": "a@m", "source": "o/one"},
				"c@m",
				map[string]any{"id": "b@m", "source": "o/one"},
			},
			source: SourceFile,
		},
		{
			name: "a full id of a listed bare name's -dev copy", content: `{"version":2,"claude":{"plugins":["agent-effort","agent-effort@agent-effort-dev"]}}`,
			err: `claude.plugins[1] agent-effort@agent-effort-dev is a copy of claude.plugins[0] agent-effort, which ensures one copy and disables the other: list the plugin once`,
		},
		{
			name: "a full id of a listed bare name's GitHub copy", content: `{"version":2,"claude":{"plugins":["agent-effort@agent-effort","c","agent-effort"]}}`,
			err: `claude.plugins[2] agent-effort is a bare name of claude.plugins[0] agent-effort@agent-effort, which it ensures or disables: list the plugin once`,
		},
		{
			name:        "a bare name beside the same plugin of another marketplace",
			content:     `{"version":2,"claude":{"plugins":["agent-effort","agent-effort@some-market"]}}`,
			want:        []ClaudePlugin{{ID: "agent-effort"}, {ID: "agent-effort@some-market"}},
			wantMarshal: []any{"agent-effort", "agent-effort@some-market"},
			source:      SourceFile,
		},
		{
			name:    "an account may not set the list",
			content: `{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"plugins":["a"]}}]}`,
			err:     `accounts[0].plugins is machine-wide: set claude.plugins`,
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
			if !reflect.DeepEqual(loaded.Claude.Plugins, tc.want) || loaded.Source(KeyClaudePlugins) != tc.source {
				t.Fatalf("plugins = %#v (%s), want %#v (%s)", loaded.Claude.Plugins, loaded.Source(KeyClaudePlugins),
					tc.want, tc.source)
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
			// The default list left unset stays out of the file (a v0.79.0 pfm refuses the key); a list
			// the file set is written back as it was.
			got, found := value.Claude["plugins"]
			if tc.source == SourceDefault {
				if found {
					t.Fatalf("Marshal wrote the unset default plugins list: %#v", got)
				}
				return
			}
			if !found || !reflect.DeepEqual(got, any(tc.wantMarshal)) {
				t.Fatalf("Marshal plugins = %#v (found %v), want %#v", got, found, tc.wantMarshal)
			}
		})
	}
}
