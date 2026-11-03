package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodeConfigReadsInstalledJSONCAndLaunchOverlay(t *testing.T) {
	fix := newFixture(t)
	root := filepath.Join(fix.home, ".config", "opencode")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "opencode.jsonc")
	if err := os.WriteFile(
		path,
		[]byte(
			`{"mcp":{"chat-professor":{"type":"local","command":["pfm","mcp"],"enabled":true}},"plugin":["/fixture/first.mjs"],}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	proc := &process{
		cwd: fix.work,
		env: fix.env(
			map[string]string{
				"XDG_CONFIG_HOME":         "",
				"OPENCODE_CONFIG_DIR":     "",
				"OPENCODE_CONFIG_CONTENT": `{"mcp":{"manual":{"type":"local","command":["manual"]}},"plugin":["/fixture/second.mjs"]}`,
			},
		),
	}
	config, err := openCodeConfig(proc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"mcp":{"chat-professor":{"command":["pfm","mcp"],"enabled":true,"type":"local"},"manual":{"command":["manual"],"type":"local"}},"plugin":["/fixture/first.mjs","/fixture/second.mjs"]}`
	if string(got) != want {
		t.Fatalf("%s: config=%s, want %s", path, got, want)
	}
}
