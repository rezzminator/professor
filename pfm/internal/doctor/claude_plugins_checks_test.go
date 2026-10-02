package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installer"
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

func TestClaudePluginsDoctorStore(t *testing.T) {
	for _, state := range []string{"clean", "disabled", "uninstalled", "absent", "settings unreadable", "record unreadable"} {
		t.Run(state, func(t *testing.T) {
			store := t.TempDir()
			path := filepath.Join(store, "settings.json")
			enabled := map[string]bool{}
			for _, id := range doctorPluginIDs {
				enabled[id] = true
			}
			if state == "disabled" {
				enabled[doctorPluginIDs[1]] = false
			}
			if state != "absent" {
				raw, _ := json.Marshal(map[string]any{"enabledPlugins": enabled})
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeDoctorInstalledPlugins(t, store, doctorPluginIDs...)
			switch state {
			case "uninstalled":
				writeDoctorInstalledPlugins(t, store, doctorPluginIDs[0], doctorPluginIDs[2])
			case "settings unreadable":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "record unreadable":
				if err := os.WriteFile(
					filepath.Join(store, "plugins", "installed_plugins.json"),
					[]byte("{"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			want := "doctor: claude_plugins ok\n"
			warnings, failures := 0, 0
			switch state {
			case "disabled":
				warnings = 1
				want = "doctor: claude_plugins plugin " + doctorPluginIDs[1] + " not enabled in " + path + " — run pfm install --yes\n"
			case "uninstalled":
				warnings = 1
				want = "doctor: claude_plugins plugin " + doctorPluginIDs[1] + " not installed in " + store + " — run pfm install --yes\n"
			case "absent":
				_, err := installer.ClaudePluginGaps(path)
				want = fmt.Sprintf("doctor: claude_plugins skipped: %v (store never set up)\n", err)
			case "settings unreadable":
				failures = 1
				_, err := installer.ClaudePluginGaps(path)
				want = fmt.Sprintf("doctor: claude_plugins could not read %s: %v\n", path, err)
			case "record unreadable":
				failures = 1
				_, err := installer.ClaudePluginsNotInstalled(store)
				want = fmt.Sprintf(
					"doctor: claude_plugins could not read %s: %v\n",
					filepath.Join(store, "plugins", "installed_plugins.json"),
					err,
				)
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudePluginsDoctor(&out, store, tally)
			if out.String() != want || tally.warnings != warnings || tally.failures != failures {
				t.Fatalf(
					"got %q tally=%+v want %q warnings=%d failures=%d",
					out.String(),
					tally,
					want,
					warnings,
					failures,
				)
			}
		})
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
