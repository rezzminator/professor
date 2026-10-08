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

var doctorDevIDs = []string{
	"cache-live-control@cache-live-control-dev",
	"sub-agent-compact@sub-agent-compact-dev",
	"agent-effort@agent-effort-dev",
}

var releaseTargets = installer.ClaudePluginTargets(installer.ClaudePluginBuild{})

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
				_, err := installer.ClaudePluginGaps(path, releaseTargets)
				want = fmt.Sprintf("doctor: claude_plugins skipped: %v (store never set up)\n", err)
			case "settings unreadable":
				failures = 1
				_, err := installer.ClaudePluginGaps(path, releaseTargets)
				want = fmt.Sprintf("doctor: claude_plugins could not read %s: %v\n", path, err)
			case "record unreadable":
				failures = 1
				_, err := installer.ClaudePluginsNotInstalled(store, releaseTargets)
				want = fmt.Sprintf(
					"doctor: claude_plugins could not read %s: %v\n",
					filepath.Join(store, "plugins", "installed_plugins.json"),
					err,
				)
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudePluginsDoctor(&out, store, installer.ClaudePluginBuild{}, tally)
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

// TestClaudePluginsDoctorChecksTheBuildsCopies: an alpha build with its
// checkouts checks the -dev ids and their copies, a fallback names its reason
// and checks the GitHub ids, and both copies enabled at once is a duplicate.
func TestClaudePluginsDoctorChecksTheBuildsCopies(t *testing.T) {
	names := []string{"cache-live-control", "sub-agent-compact", "agent-effort"}
	for _, state := range []string{
		"alpha clean", "alpha duplicate", "alpha copy symlinked", "alpha copy missing", "alpha fallback", "release duplicate",
	} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			store := filepath.Join(home, ".claude")
			root := filepath.Join(home, "src")
			build := installer.ClaudePluginBuild{Version: "0.80.0-alpha", Home: home, CheckoutRoot: root}
			if state == "alpha fallback" {
				build.CheckoutRoot = ""
			}
			if state == "release duplicate" {
				build.Version = "0.80.0"
			}
			mirror := func(name string) string {
				return filepath.Join(home, ".local", "share", name+"-dev", "plugins", name)
			}
			for _, name := range names {
				checkout := filepath.Join(root, name, "plugins", name)
				for _, dir := range []string{filepath.Join(checkout, ".claude-plugin"), mirror(name)} {
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(
					filepath.Join(checkout, ".claude-plugin", "plugin.json"),
					[]byte("{}"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			switch state {
			case "alpha copy symlinked":
				if err := os.Remove(mirror(names[0])); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, names[0], "plugins", names[0]), mirror(names[0])); err != nil {
					t.Fatal(err)
				}
			case "alpha copy missing":
				if err := os.Remove(mirror(names[0])); err != nil {
					t.Fatal(err)
				}
			}
			enabled := map[string]bool{}
			ensured := doctorDevIDs
			if state == "alpha fallback" || state == "release duplicate" {
				ensured = doctorPluginIDs
			}
			for index := range names {
				enabled[doctorPluginIDs[index]] = false
				enabled[ensured[index]] = true
			}
			if state == "alpha duplicate" {
				enabled[doctorPluginIDs[0]] = true
			}
			if state == "release duplicate" {
				enabled[doctorDevIDs[0]] = true
			}
			if err := os.MkdirAll(filepath.Join(store, "plugins"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store, "settings.json")
			raw, _ := json.Marshal(map[string]any{"enabledPlugins": enabled})
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			writeDoctorInstalledPlugins(t, store, ensured...)
			want, warnings := "doctor: claude_plugins ok\n", 0
			twice := "doctor: claude_plugins plugin cache-live-control enabled twice (" + doctorPluginIDs[0] + " and " +
				doctorDevIDs[0] + ") in " + path
			switch state {
			case "alpha duplicate", "release duplicate":
				want, warnings = twice+" — run pfm install --yes\n", 1
			case "alpha copy symlinked":
				want, warnings = "doctor: claude_plugins plugin "+doctorDevIDs[0]+" copy "+mirror(names[0])+
					" is a symlink: Claude refuses plugin files outside its marketplace — run pfm install --yes\n", 1
			case "alpha copy missing":
				want, warnings = "doctor: claude_plugins plugin "+doctorDevIDs[0]+" copy "+mirror(names[0])+
					" missing — run pfm install --yes\n", 1
			case "alpha fallback":
				want = ""
				for index, name := range names {
					want += "doctor: claude_plugins " + name + ": alpha build falls back to the GitHub copy " +
						doctorPluginIDs[index] + " — claude.pluginCheckoutRoot unset\n"
				}
				want += "doctor: claude_plugins ok\n"
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudePluginsDoctor(&out, store, build, tally)
			if out.String() != want || tally.warnings != warnings || tally.failures != 0 {
				t.Fatalf("got %q tally=%+v\nwant %q warnings=%d", out.String(), tally, want, warnings)
			}
		})
	}
}
