package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The seed a first install writes leaves out the new claude keys a pfm of the
// last release refuses (remoteControl, pluginCheckoutRoot, plugins, costLedger, settings, and the
// top-level envs, harnessPrompt and herdr) while they sit at their defaults, exactly as `pfm config init` does
// (marshalClaude); the example file itself keeps them, for its parity test.
func TestInstallConfigSeedOmitsDefaultKeysAnOlderPfmRefuses(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find test source")
	}
	example, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "example.pfm.config.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, claude := installSeedClaude(t, example)
	for _, key := range []string{"remoteControl", "pluginCheckoutRoot", "plugins", "costLedger", "settings"} {
		if _, present := claude[key]; present {
			t.Errorf("the seed keeps claude.%s at its default", key)
		}
	}
	for _, key := range []string{"envs", "harnessPrompt", "herdr"} {
		if _, present := document[key]; present {
			t.Errorf("the seed keeps %s at its default", key)
		}
	}
	if string(document["version"]) != "2" {
		t.Errorf("the seed lost its version: %s", document["version"])
	}
	if string(claude["permissionMode"]) != `"bypass"` || string(claude["binary"]) != `"claude"` {
		t.Errorf("the seed lost the other claude keys: %s", claude)
	}
}

// A value the example sets away from its default is kept.
func TestInstallConfigSeedKeepsNonDefaultNewKeys(t *testing.T) {
	document, claude := installSeedClaude(t, []byte(`{"version":2,"envs":{"TEAM_MODE":"strict"},`+
		`"harnessPrompt":{"replace":"team.md"},"herdr":{"keys":{"close_pane":null}},"claude":{"settings":{"cleanupPeriodDays":30},"costLedger":false,`+
		`"plugins":["auto-effort"],"remoteControl":false,"theme":null}}`))
	for key, want := range map[string]string{
		"envs": `{"TEAM_MODE":"strict"}`, "harnessPrompt": `{"replace":"team.md"}`, "herdr": `{"keys":{"close_pane":null}}`,
	} {
		if got := strings.Join(strings.Fields(string(document[key])), ""); got != want {
			t.Errorf("seed %s = %q, want %q", key, got, want)
		}
	}
	for key, want := range map[string]string{
		"settings": `{"cleanupPeriodDays":30}`, "costLedger": "false", "plugins": `["auto-effort"]`,
		"remoteControl": "false",
	} {
		if got := strings.Join(strings.Fields(string(claude[key])), ""); got != want {
			t.Errorf("seed claude.%s = %q, want %q", key, got, want)
		}
	}
}

// snapshotTree records every path under root with its mode and content, so a read-only call can be proven to leave the tree as it was.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot[relative] = fmt.Sprintf("symlink:%o:%s", info.Mode().Perm(), target)
		case info.IsDir():
			snapshot[relative] = fmt.Sprintf("dir:%o", info.Mode().Perm())
		default:
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[relative] = fmt.Sprintf("file:%o:%x", info.Mode().Perm(), content)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
