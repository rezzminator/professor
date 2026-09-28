package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

var doctorPluginIDs = []string{
	"cache-live-control@cache-live-control", "sub-agent-compact@sub-agent-compact", "agent-effort@agent-effort",
}

// writeDoctorInstalledPlugins records ids in dir's plugins/installed_plugins.json
// the way claude does, each with an installPath that exists.
func writeDoctorInstalledPlugins(t *testing.T, dir string, ids ...string) {
	t.Helper()
	plugins := map[string]any{}
	for _, id := range ids {
		installPath := filepath.Join(dir, "plugins", "cache", id)
		if err := os.MkdirAll(installPath, 0o700); err != nil {
			t.Fatal(err)
		}
		plugins[id] = []any{map[string]any{"installPath": installPath}}
	}
	raw, err := json.Marshal(map[string]any{"version": 2, "plugins": plugins})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudePluginsDoctorNamesEachGapPerAccount(t *testing.T) {
	home := t.TempDir()
	complete := filepath.Join(home, ".claude")
	partial := filepath.Join(home, ".cc", "2")
	broken := filepath.Join(home, ".cc", "3")
	fresh := filepath.Join(home, ".cc", "4")
	shared := filepath.Join(home, ".cc", "5")
	for _, dir := range []string{complete, partial, broken, fresh, shared} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeSettings := func(dir, content string) {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSettings(complete, `{"enabledPlugins":{"cache-live-control@cache-live-control":true,`+
		`"sub-agent-compact@sub-agent-compact":true,"agent-effort@agent-effort":true}}`)
	writeDoctorInstalledPlugins(t, complete, doctorPluginIDs...)
	writeSettings(partial, `{"enabledPlugins":{"cache-live-control@cache-live-control":true,`+
		`"sub-agent-compact@sub-agent-compact":false,"agent-effort@agent-effort":true}}`)
	writeDoctorInstalledPlugins(t, partial, doctorPluginIDs...)
	// A directory where the file belongs fails the read for any uid.
	if err := os.MkdirAll(filepath.Join(broken, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Account 5 shares account 1's settings.json, as a symlinked seat does,
	// but its own config dir installed only one plugin.
	if err := os.Symlink(filepath.Join(complete, "settings.json"), filepath.Join(shared, "settings.json")); err != nil {
		t.Fatal(err)
	}
	writeDoctorInstalledPlugins(t, shared, doctorPluginIDs[0])
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{
		{ID: 1, ConfigDir: complete},
		{ID: 2, ConfigDir: partial},
		{ID: 3, ConfigDir: broken},
		{ID: 4, ConfigDir: fresh},
		{ID: 5, ConfigDir: shared},
	}}
	var output bytes.Buffer
	tally := &doctorTally{}
	printClaudePluginsDoctor(&output, machine, tally)
	// The unreadable file is a failure; each missing plugin is a warning.
	if tally.failures != 1 || tally.warnings != 3 {
		t.Fatalf("failures=%d warnings=%d, want 1 and 3\n%s", tally.failures, tally.warnings, output.String())
	}
	for _, want := range []string{
		"doctor: claude_plugins claude[1] ok\n",
		"doctor: claude_plugins claude[2] plugin sub-agent-compact@sub-agent-compact not enabled — run pfm",
		"doctor: claude_plugins claude[3] could not read " + filepath.Join(broken, "settings.json"),
		"doctor: claude_plugins claude[4] skipped: no settings.json at " + filepath.Join(fresh, "settings.json"),
		"doctor: claude_plugins claude[5] plugin sub-agent-compact@sub-agent-compact not installed in " + shared +
			" — run pfm install --yes",
		"doctor: claude_plugins claude[5] plugin agent-effort@agent-effort not installed in " + shared +
			" — run pfm install --yes",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "claude[2] plugin cache-live-control") ||
		strings.Contains(output.String(), "env CLAUDE_CODE_AUTO_COMPACT_WINDOW absent") ||
		strings.Contains(output.String(), "claude[5] plugin cache-live-control") {
		t.Fatalf("a present plugin or removed env check was reported missing:\n%s", output.String())
	}
}

func TestClaudePluginsDoctorDedupesMissingTailThroughSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	if err := os.Mkdir(physical, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	if !firstVisit(seen, filepath.Join(alias, "settings.json")) ||
		firstVisit(seen, filepath.Join(physical, "settings.json")) || len(seen) != 1 {
		t.Fatalf("missing settings target visited more than once: %v", seen)
	}
}
