package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

var doctorPluginIDs = []string{
	"cache-live-control@cache-live-control",
	"sub-agent-compact@sub-agent-compact",
	"effort-live-control@effort-live-control",
	"tool-visibility-controller@tool-visibility-controller",
}

var doctorDevIDs = []string{
	"cache-live-control@cache-live-control-dev",
	"sub-agent-compact@sub-agent-compact-dev",
	"effort-live-control@effort-live-control-dev",
	"tool-visibility-controller@tool-visibility-controller-dev",
}

var releaseTargets = installer.ClaudePluginTargets(
	installer.ClaudePluginBuild{Plugins: pfmconfig.DefaultClaudePlugins()},
)

// bareNameFix is what doctor tells for a bare plugin name of ours that is not enabled or not
// installed: the repair, the source it installs from and what a persistent failure means.
func bareNameFix(name string) string {
	return "run pfm install --yes; if it keeps failing, " + installer.BareClaudePluginRepair(name)
}

// writeDoctorCache records id at version in dir's plugins/installed_plugins.json,
// which writeDoctorInstalledPlugins wrote, its installPath holding files: the
// snapshot Claude runs it from.
func writeDoctorCache(t *testing.T, dir, id, version string, files map[string]string) {
	t.Helper()
	path := filepath.Join(dir, "plugins", "installed_plugins.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Version int                         `json:"version"`
		Plugins map[string][]map[string]any `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	for _, entry := range record.Plugins[id] {
		entry["version"] = version
		installPath, _ := entry["installPath"].(string)
		for rel, content := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(installPath, rel)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(installPath, rel), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if raw, err = json.Marshal(record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeDoctorInstalledPlugins records ids in dir's plugins/installed_plugins.json
// the way claude does, each a user entry with an installPath that exists.
func writeDoctorInstalledPlugins(t *testing.T, dir string, ids ...string) {
	t.Helper()
	plugins := map[string]any{}
	for _, id := range ids {
		installPath := filepath.Join(dir, "plugins", "cache", id)
		if err := os.MkdirAll(installPath, 0o700); err != nil {
			t.Fatal(err)
		}
		plugins[id] = []any{map[string]any{"scope": "user", "installPath": installPath}}
	}
	raw, err := json.Marshal(map[string]any{"version": 2, "plugins": plugins})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestClaudePluginsDoctorNamesAPluginItCannotInstall: a listed third-party id whose marketplace
// Claude Code does not know and whose entry has no source is one warning naming it, the reason
// and the repair, in place of the generic "run pfm install --yes" lines that would not help, even
// beside the skipped row of a store with no settings.json; with a source it is the generic lines;
// an id installed and enabled needs no marketplace; and an unreadable marketplace record is a
// failure for the ids that need it while every other listed plugin is still checked.
func TestClaudePluginsDoctorNamesAPluginItCannotInstall(t *testing.T) {
	cannot := "doctor: claude_plugins plugin typesafe@typesafe-ai cannot be installed: marketplace typesafe-ai" +
		" is not known to Claude Code and its claude.plugins entry has no source — " +
		installer.ClaudePluginUnresolvedFix + "\n"
	for _, test := range []struct {
		name               string
		plugin             pfmconfig.ClaudePlugin
		noSettings         bool
		known              string
		want               func(path, store string) string
		warnings, failures int
	}{
		{
			"no source",
			pfmconfig.ClaudePlugin{ID: "typesafe@typesafe-ai"},
			false, `{"some-other":{}}`,
			func(string, string) string { return cannot }, 1, 0,
		},
		{
			"no source and no settings.json",
			pfmconfig.ClaudePlugin{ID: "typesafe@typesafe-ai"},
			true, `{"some-other":{}}`,
			func(path, _ string) string {
				// Nothing is enabled in a store never set up: the installed id needs its marketplace too.
				return "doctor: claude_plugins skipped: no settings.json at " + path + " (store never set up)\n" + cannot +
					"doctor: claude_plugins plugin installed@gone-market cannot be installed: marketplace gone-market" +
					" is not known to Claude Code and its claude.plugins entry has no source — " +
					installer.ClaudePluginUnresolvedFix + "\n"
			}, 2, 0,
		},
		{"with a source", pfmconfig.ClaudePlugin{ID: "typesafe@typesafe-ai", Source: "typesafe-ai/skills"}, false, `{"some-other":{}}`, func(path, store string) string {
			return "doctor: claude_plugins plugin typesafe@typesafe-ai not enabled in " + path + " — run pfm install --yes\n" +
				"doctor: claude_plugins plugin typesafe@typesafe-ai not installed in " + store + " — run pfm install --yes\n"
		}, 2, 0},
		{
			"an unreadable marketplace record",
			pfmconfig.ClaudePlugin{ID: "effort-live-control"},
			false, `{`,
			func(path, store string) string {
				return "doctor: claude_plugins could not read the plugin records in " + store + ": parse " +
					filepath.Join(store, "plugins", "known_marketplaces.json") + ": unexpected end of JSON input\n" +
					"doctor: claude_plugins plugin effort-live-control@effort-live-control not enabled in " + path + " — " + bareNameFix("effort-live-control") + "\n" +
					"doctor: claude_plugins plugin effort-live-control@effort-live-control not installed in " + store + " — " + bareNameFix("effort-live-control") + "\n" +
					"doctor: claude_plugins plugin typesafe@typesafe-ai cannot be checked: marketplace typesafe-ai cannot be looked up" +
					" while the plugin records are unreadable, and its claude.plugins entry has no source — " +
					"repair the unreadable file named above, then run pfm doctor again\n"
			}, 3, 1,
		},
		{
			"a marketplace record of the wrong type",
			pfmconfig.ClaudePlugin{ID: "typesafe@typesafe-ai", Source: "typesafe-ai/skills"},
			false, `[]`,
			func(path, store string) string {
				// The one unreadable file behind both reads is one failure.
				wrongType := json.Unmarshal([]byte(`[]`), &map[string]json.RawMessage{})
				return "doctor: claude_plugins could not read the plugin records in " + store + ": parse " +
					filepath.Join(store, "plugins", "known_marketplaces.json") + ": " + wrongType.Error() + "\n" +
					"doctor: claude_plugins plugin typesafe@typesafe-ai not enabled in " + path + " — run pfm install --yes\n" +
					"doctor: claude_plugins plugin typesafe@typesafe-ai not installed in " + store + " — run pfm install --yes\n"
			}, 2, 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := t.TempDir()
			path := filepath.Join(store, "settings.json")
			if !test.noSettings {
				raw, _ := json.Marshal(map[string]any{"enabledPlugins": map[string]bool{"installed@gone-market": true}})
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeDoctorInstalledPlugins(t, store, "installed@gone-market")
			if err := os.WriteFile(
				filepath.Join(store, "plugins", "known_marketplaces.json"),
				[]byte(test.known),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			plugins := []pfmconfig.ClaudePlugin{test.plugin, {ID: "installed@gone-market"}}
			if test.known == `{` {
				plugins = append(plugins, pfmconfig.ClaudePlugin{ID: "typesafe@typesafe-ai"})
			}
			printClaudePluginsDoctor(&out, store, &paths.MapEnv{}, installer.ClaudePluginBuild{Plugins: plugins}, tally)
			want := test.want(path, store)
			if out.String() != want || tally.warnings != test.warnings || tally.failures != test.failures {
				t.Fatalf("got %q tally=%+v\nwant %q warnings=%d failures=%d",
					out.String(), tally, want, test.warnings, test.failures)
			}
		})
	}
}

func TestClaudePluginsDoctorStore(t *testing.T) {
	for _, state := range []string{
		"clean", "disabled", "uninstalled", "absent", "settings unreadable", "record unreadable",
		"unmanaged duplicate", "managed plus a stranger", "foreign marketplace", "marketplaces unreadable",
	} {
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
			if state == "unmanaged duplicate" {
				enabled["buddy@buddy"] = true
				enabled["buddy@buddy-dev"] = true
				enabled["callmeter@callmeter-dev"] = true
				enabled["solo@one"] = true
				enabled["solo@two"] = false
			}
			if state == "managed plus a stranger" {
				enabled["cache-live-control@elsewhere"] = true
			}
			if state != "absent" {
				raw, _ := json.Marshal(map[string]any{"enabledPlugins": enabled})
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeDoctorInstalledPlugins(t, store, doctorPluginIDs...)
			marketplaces := filepath.Join(store, "plugins", "known_marketplaces.json")
			switch state {
			case "uninstalled":
				writeDoctorInstalledPlugins(t, store, doctorPluginIDs[0], doctorPluginIDs[2], doctorPluginIDs[3])
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
			case "foreign marketplace":
				raw, _ := json.Marshal(map[string]any{
					"cache-live-control": map[string]any{
						"source": map[string]any{"source": "github", "repo": "someone-else/cache-live-control"},
					},
					"sub-agent-compact": map[string]any{
						"source": map[string]any{"source": "github", "repo": "rezzminator/sub-agent-compact"},
					},
					"buddy": map[string]any{"source": map[string]any{"source": "github", "repo": "someone-else/buddy"}},
				})
				if err := os.WriteFile(marketplaces, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "marketplaces unreadable":
				if err := os.WriteFile(marketplaces, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want := "doctor: claude_plugins ok\n"
			warnings, failures := 0, 0
			switch state {
			case "disabled":
				warnings = 1
				want = "doctor: claude_plugins plugin " + doctorPluginIDs[1] + " not enabled in " + path + " — " +
					bareNameFix("sub-agent-compact") + "\n"
			case "uninstalled":
				warnings = 1
				want = "doctor: claude_plugins plugin " + doctorPluginIDs[1] + " not installed in " + store + " — " +
					bareNameFix("sub-agent-compact") + "\n"
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
			case "unmanaged duplicate":
				warnings = 1
				want = "doctor: claude_plugins plugin buddy enabled twice (buddy@buddy and buddy@buddy-dev) in " + path +
					" — keep one copy: claude plugin disable each other key\n"
			case "managed plus a stranger":
				warnings = 1
				want = "doctor: claude_plugins plugin cache-live-control enabled twice (" + doctorPluginIDs[0] +
					" and cache-live-control@elsewhere) in " + path + " — keep one copy: claude plugin disable each other key\n"
			case "foreign marketplace":
				warnings = 1
				want = "doctor: claude_plugins plugin " + doctorPluginIDs[0] + " marketplace cache-live-control in " +
					marketplaces + " points at someone-else/cache-live-control, want rezzminator/cache-live-control" +
					" — claude plugin marketplace remove cache-live-control, then run pfm install --yes\n"
			case "marketplaces unreadable":
				failures = 1
				_, err := installer.ClaudePluginMarketplaceGaps(store, releaseTargets)
				want = fmt.Sprintf("doctor: claude_plugins could not read %s: %v\n", marketplaces, err)
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudePluginsDoctor(
				&out,
				store,
				&paths.MapEnv{},
				installer.ClaudePluginBuild{Plugins: pfmconfig.DefaultClaudePlugins()},
				tally,
			)
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

// TestClaudePluginsDoctorBareNameNamesItsSource: a bare claude.plugins name that is not enabled or
// not installed is told where pfm install gets it from and that a failing install means the entry
// is wrong, not only "run pfm install --yes" (a typo installs from no repository, so that repair
// can never close the gap). An -alpha build that falls back to the GitHub copy says the same.
func TestClaudePluginsDoctorBareNameNamesItsSource(t *testing.T) {
	// On -alpha a listed plugin off its -dev copy fails: each line is marked FAIL.
	for _, test := range []struct {
		name, version, mark, note string
	}{
		{"release", "", "", ""},
		{"alpha falls back to GitHub", "0.80.0-alpha", "FAIL ", "doctor: claude_plugins FAIL plugin agent-efort falls back to the GitHub copy" +
			" agent-efort@agent-efort on this alpha build: claude.pluginCheckoutRoot unset and no source clone — " +
			"check the plugin out where named, or set claude.pluginCheckoutRoot, then run pfm install --yes\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := t.TempDir()
			path := filepath.Join(store, "settings.json")
			raw, _ := json.Marshal(map[string]any{"enabledPlugins": map[string]bool{}})
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(store, "plugins"), 0o700); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudePluginsDoctor(&out, store, &paths.MapEnv{}, installer.ClaudePluginBuild{
				Version: test.version, Home: t.TempDir(), Plugins: []pfmconfig.ClaudePlugin{{ID: "agent-efort"}},
			}, tally)
			want := "doctor: claude_plugins " + test.mark + "plugin agent-efort@agent-efort not enabled in " + path + " — " +
				bareNameFix(
					"agent-efort",
				) + "\n" +
				"doctor: claude_plugins " + test.mark + "plugin agent-efort@agent-efort not installed in " + store + " — " +
				bareNameFix(
					"agent-efort",
				) + "\n" + test.note
			warnings, failures := 2, 0
			if test.mark != "" {
				warnings, failures = 0, 3
			}
			if out.String() != want || tally.warnings != warnings || tally.failures != failures {
				t.Fatalf(
					"got %q tally=%+v\nwant %q warnings=%d failures=%d",
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

// TestClaudePluginsDoctorNamesIdsWhilePluginRecordsAreUnreadable: the ids a listed entry needs a
// known marketplace for come from claude.plugins, not from the unreadable records, so doctor names
// each one it cannot verify beside the failure naming the file: a settings.json that is unreadable
// as well, and the one id the readable records show enabled and installed, change nothing about
// that; an id with a source needs no known marketplace and is never named here.
func TestClaudePluginsDoctorNamesIdsWhilePluginRecordsAreUnreadable(t *testing.T) {
	cannotCheck := func(id, marketplace string) string {
		return "doctor: claude_plugins plugin " + id + " cannot be checked: marketplace " + marketplace +
			" cannot be looked up while the plugin records are unreadable, and its claude.plugins entry has no source — " +
			"repair the unreadable file named above, then run pfm doctor again\n"
	}
	for _, state := range []string{"known_marketplaces.json unreadable", "settings.json unreadable"} {
		t.Run(state, func(t *testing.T) {
			store := t.TempDir()
			path := filepath.Join(store, "settings.json")
			known := filepath.Join(store, "plugins", "known_marketplaces.json")
			raw, _ := json.Marshal(map[string]any{"enabledPlugins": map[string]bool{"fine@fine-market": true}})
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			writeDoctorInstalledPlugins(t, store, "fine@fine-market")
			if err := os.WriteFile(known, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			var want string
			var unreadable error
			if state == "settings.json unreadable" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				_, unreadable = installer.ClaudePluginGaps(path, nil)
			}
			plugins := []pfmconfig.ClaudePlugin{
				{ID: "first@one-market"},
				{ID: "fine@fine-market"},
				{ID: "sourced@two-market", Source: "o/two"},
				{ID: "last@three-market"},
			}
			build := installer.ClaudePluginBuild{Plugins: plugins}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudePluginsDoctor(&out, store, &paths.MapEnv{}, build, tally)
			_, recordErr := installer.ClaudePluginsUnresolved(store, installer.ClaudePluginTargets(build))
			records := "doctor: claude_plugins could not read the plugin records in " + store + ": " + recordErr.Error() + "\n"
			first, fine, last := cannotCheck("first@one-market", "one-market"),
				cannotCheck("fine@fine-market", "fine-market"), cannotCheck("last@three-market", "three-market")
			switch state {
			case "known_marketplaces.json unreadable":
				want = records +
					"doctor: claude_plugins plugin sourced@two-market not enabled in " + path + " — run pfm install --yes\n" +
					"doctor: claude_plugins plugin sourced@two-market not installed in " + store + " — run pfm install --yes\n" +
					first + last
				if tally.warnings != 4 || tally.failures != 1 {
					t.Fatalf("tally=%+v, want 4 warnings 1 failure\n%s", tally, out.String())
				}
			case "settings.json unreadable":
				want = records + first + fine + last +
					"doctor: claude_plugins could not read " + path + ": " + unreadable.Error() + "\n"
				if tally.warnings != 3 || tally.failures != 2 {
					t.Fatalf("tally=%+v, want 3 warnings 2 failures\n%s", tally, out.String())
				}
			}
			if out.String() != want {
				t.Fatalf("got %q\nwant %q", out.String(), want)
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
// checkouts checks the -dev ids and their copies — each gap of a listed plugin
// a FAIL — a fallback names its reason and fails, two copies enabled at once is
// a duplicate, and a marketplace plugin claude.plugins does not list is
// checked installed with no copy enabled, a warning when enabled.
func TestClaudePluginsDoctorChecksTheBuildsCopies(t *testing.T) {
	names := []string{"cache-live-control", "sub-agent-compact", "effort-live-control", "tool-visibility-controller"}
	for _, state := range []string{
		"alpha clean", "alpha duplicate", "alpha copy symlinked", "alpha copy missing", "alpha fallback", "release duplicate",
		"alpha from the default source clone", "alpha source clone moved away", "alpha copy differs",
		"alpha unlisted plugin disabled", "alpha unlisted plugin enabled", "alpha cache behind", "alpha cache differs",
		"alpha cache unreadable", "alpha cache unversioned", "alpha cache behind with its snapshot gone",
	} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			store := filepath.Join(home, ".claude")
			root := filepath.Join(home, "src")
			build := installer.ClaudePluginBuild{
				Version: "0.80.0-alpha", Home: home, CheckoutRoot: root, Plugins: pfmconfig.DefaultClaudePlugins(),
			}
			if state == "alpha from the default source clone" {
				// No marker and no key: the clone at the default path holds the checkouts.
				root = filepath.Join(home, ".professor", "plugins")
				build = doctorClaudePluginBuild(
					"0.80.0-alpha", home, pfmconfig.Claude{Plugins: pfmconfig.DefaultClaudePlugins()}, false,
				)
			}
			if state == "alpha fallback" {
				// No marker, no key and no clone at the default path: the checkouts under src/ are not looked at.
				build = doctorClaudePluginBuild(
					"0.80.0-alpha", home, pfmconfig.Claude{Plugins: pfmconfig.DefaultClaudePlugins()}, false,
				)
			}
			if state == "alpha source clone moved away" {
				// The -dev copies a prior install enabled stay; the clone it recorded is gone.
				clone := filepath.Join(home, "moved")
				if err := os.MkdirAll(clone, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(clone); err != nil {
					t.Fatal(err)
				}
				build = doctorClaudePluginBuild(
					"0.80.0-alpha", home, pfmconfig.Claude{Plugins: pfmconfig.DefaultClaudePlugins()}, false,
				)
				if build.SourceRepoErr == nil {
					t.Fatalf("build = %+v, want the moved clone's resolve error", build)
				}
			}
			if state == "release duplicate" {
				build.Version = "0.80.0"
			}
			unlisted := strings.HasPrefix(state, "alpha unlisted")
			if unlisted {
				// The source clone's marketplace lists buddy, which claude.plugins does not.
				build.SourceRepo = filepath.Join(home, "clone")
				entries := []any{}
				for _, name := range append(slices.Clone(names), "buddy") {
					entries = append(entries, map[string]any{"name": name, "source": "./plugins/" + name})
				}
				manifest, _ := json.Marshal(map[string]any{"name": "professor", "plugins": entries})
				if err := os.MkdirAll(filepath.Join(build.SourceRepo, ".claude-plugin"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(build.SourceRepo, ".claude-plugin", "marketplace.json"),
					manifest,
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			mirror := func(name string) string {
				return filepath.Join(home, ".local", "share", name+"-dev", "plugins", name)
			}
			// Each copy holds its checkout's content.
			for _, name := range append(slices.Clone(names), "buddy") {
				content := `{"version":"1.0.0"}`
				if state == "alpha cache unversioned" && name == names[0] {
					content = "{}"
				}
				for _, dir := range []string{filepath.Join(root, name, "plugins", name), mirror(name)} {
					if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(
						filepath.Join(dir, ".claude-plugin", "plugin.json"),
						[]byte(content),
						0o600,
					); err != nil {
						t.Fatal(err)
					}
				}
			}
			switch state {
			case "alpha copy symlinked":
				if err := os.RemoveAll(mirror(names[0])); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, names[0], "plugins", names[0]), mirror(names[0])); err != nil {
					t.Fatal(err)
				}
			case "alpha copy missing":
				if err := os.RemoveAll(mirror(names[0])); err != nil {
					t.Fatal(err)
				}
			case "alpha copy differs":
				if err := os.WriteFile(
					filepath.Join(mirror(names[0]), ".claude-plugin", "plugin.json"),
					[]byte("{\"old\":1}"),
					0o600,
				); err != nil {
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
			if unlisted {
				enabled["buddy@buddy-dev"] = state == "alpha unlisted plugin enabled"
				ensured = append(slices.Clone(ensured), "buddy@buddy-dev")
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
			manifest := filepath.Join(".claude-plugin", "plugin.json")
			for _, id := range ensured {
				// Claude's snapshot of each -dev copy holds its checkout's content.
				if name, marketplace, _ := strings.Cut(id, "@"); marketplace == name+"-dev" {
					writeDoctorCache(t, store, id, "1.0.0", map[string]string{manifest: `{"version":"1.0.0"}`})
				}
			}
			cache := filepath.Join(store, "plugins", "cache", doctorDevIDs[0])
			checkout := filepath.Join(root, names[0], "plugins", names[0])
			switch state {
			case "alpha cache behind", "alpha cache behind with its snapshot gone":
				// The checkout moved to 2.0.0; Claude still runs its 1.0.0 snapshot.
				for _, dir := range []string{checkout, mirror(names[0])} {
					if err := os.WriteFile(
						filepath.Join(dir, manifest),
						[]byte(`{"version":"2.0.0"}`),
						0o600,
					); err != nil {
						t.Fatal(err)
					}
				}
				writeDoctorCache(t, store, doctorDevIDs[0], "1.0.0", map[string]string{manifest: `{"version":"2.0.0"}`})
				if state == "alpha cache behind with its snapshot gone" {
					// Not installed is the one gap: its install is the repair.
					if err := os.RemoveAll(cache); err != nil {
						t.Fatal(err)
					}
				}
			case "alpha cache unreadable":
				if err := os.Symlink(filepath.Join(home, "nowhere"), filepath.Join(cache, "gone")); err != nil {
					t.Fatal(err)
				}
			case "alpha cache differs":
				writeDoctorCache(t, store, doctorDevIDs[0], "1.0.0", map[string]string{
					manifest: "{\"old\":1}", filepath.Join("node_modules", "dep.js"): "x", ".orphaned_at": "1",
				})
			}
			want, warnings, failures := "doctor: claude_plugins ok\n", 0, 0
			twice := "plugin cache-live-control enabled twice (" + doctorPluginIDs[0] + " and " + doctorDevIDs[0] + ") in " + path
			switch state {
			case "alpha duplicate":
				want, failures = "doctor: claude_plugins FAIL "+twice+" — run pfm install --yes\n", 1
			case "release duplicate":
				want, warnings = "doctor: claude_plugins "+twice+" — run pfm install --yes\n", 1
			case "alpha copy symlinked":
				want, failures = "doctor: claude_plugins FAIL plugin "+doctorDevIDs[0]+" copy "+mirror(names[0])+
					" is a symlink: Claude refuses plugin files outside its marketplace — run pfm install --yes\n", 1
			case "alpha copy missing":
				want, failures = "doctor: claude_plugins FAIL plugin "+doctorDevIDs[0]+" copy "+mirror(names[0])+
					" missing — run pfm install --yes\n", 1
			case "alpha copy differs":
				want, failures = "doctor: claude_plugins FAIL plugin "+doctorDevIDs[0]+" copy "+mirror(names[0])+
					" differs from "+filepath.Join(root, names[0], "plugins", names[0])+" — run pfm install --yes\n", 1
			case "alpha cache behind":
				want, failures = "doctor: claude_plugins FAIL plugin "+doctorDevIDs[0]+" cache in "+
					filepath.Join(
						store,
						"plugins",
						"installed_plugins.json",
					)+" records version 1.0.0 (scope user), checkout "+
					checkout+" is 2.0.0 — run pfm install --yes\n", 1
			case "alpha cache unreadable":
				want, failures = "doctor: claude_plugins could not read the plugin cache: plugin "+doctorDevIDs[0]+
					" cache: read cache "+cache+": stat "+filepath.Join(cache, "gone")+": no such file or directory\n", 1
			case "alpha cache differs":
				want, failures = "doctor: claude_plugins FAIL plugin "+doctorDevIDs[0]+" cache "+cache+" differs from "+
					checkout+" at version 1.0.0 — bump the version in "+filepath.Join(checkout, manifest)+
					": Claude caches by version; then run pfm install --yes\n", 1
			case "alpha cache unversioned":
				record := filepath.Join(store, "plugins", "installed_plugins.json")
				plugin := filepath.Join(checkout, manifest)
				want, failures = "doctor: claude_plugins FAIL plugin "+doctorDevIDs[0]+" cache in "+record+
					" cannot be checked: "+plugin+" names no version, and Claude caches by version — add a version to "+
					plugin+", then run pfm install --yes\n", 1
			case "alpha cache behind with its snapshot gone":
				want, failures = "doctor: claude_plugins FAIL plugin "+doctorDevIDs[0]+" not installed in "+store+
					" — run pfm install --yes\n", 1
			case "alpha unlisted plugin enabled":
				want, warnings = "doctor: claude_plugins plugin buddy enabled (buddy@buddy-dev) in "+path+
					", but claude.plugins does not list it — run pfm install --yes\n", 1
			case "alpha fallback":
				want, failures = "", len(names)
				for index, name := range names {
					checkout := filepath.Join(home, ".professor", "plugins", name, "plugins", name)
					want += "doctor: claude_plugins FAIL plugin " + name + " falls back to the GitHub copy " +
						doctorPluginIDs[index] + " on this alpha build: no checkout at " +
						filepath.Join(checkout, ".claude-plugin", "plugin.json") +
						" (the source clone's plugins/; claude.pluginCheckoutRoot unset) — " +
						"check the plugin out where named, or set claude.pluginCheckoutRoot, then run pfm install --yes\n"
				}
			case "alpha source clone moved away":
				want, failures = "doctor: claude_plugins marketplace plugin list unread: source clone unresolved: "+
					build.SourceRepoErr.Error()+" — repair it, then run pfm install --yes\n", len(names)+1
				for index, name := range names {
					want += "doctor: claude_plugins FAIL plugin " + name + " cannot be checked: its copy is undecided (" +
						doctorPluginIDs[index] + " or " + doctorDevIDs[index] +
						"): source clone unresolved (claude.pluginCheckoutRoot unset): " + build.SourceRepoErr.Error() +
						" — repair the path named, or run pfm install --yes from inside your Professor clone to record it again\n"
				}
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudePluginsDoctor(&out, store, &paths.MapEnv{}, build, tally)
			if out.String() != want || tally.warnings != warnings || tally.failures != failures {
				t.Fatalf(
					"got %q tally=%+v\nwant %q warnings=%d failures=%d",
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

// TestDoctorClaudePluginBuild: doctor checks the plugins of the build install
// ensures — claude.pluginCheckoutRoot and the list from config, and the source
// clone install recorded in its marker, whose plugins/ an -alpha build reads
// when the key is unset. The default clone and a recorded clone that moved
// away are states of TestClaudePluginsDoctorChecksTheBuildsCopies.
func TestDoctorClaudePluginBuild(t *testing.T) {
	plugins := []pfmconfig.ClaudePlugin{{ID: "callmeter"}}
	home := t.TempDir()
	clone := filepath.Join(home, "src", "professor")
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	// The marker is written the way install records its clone.
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	// The marker records the clone's physical path.
	clone, err := filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}
	build := doctorClaudePluginBuild(
		"0.80.0-alpha", home, pfmconfig.Claude{PluginCheckoutRoot: "/opt/plugins", Plugins: plugins}, true,
	)
	if build.Version != "0.80.0-alpha" || build.Home != home || build.CheckoutRoot != "/opt/plugins" ||
		!build.PluginsDefault ||
		!reflect.DeepEqual(build.Plugins, plugins) ||
		build.SourceRepo != clone ||
		build.SourceRepoErr != nil {
		t.Fatalf("build = %+v, want source clone %q", build, clone)
	}
}

// TestClaudePluginsDoctorChecksTheCallmeterBinary: an enabled callmeter -dev
// copy runs the binary its wrapper finds at {CALLMETER_HOME}/bin/{plugin.json
// version}/callmeter; on an -alpha build doctor fails when the binary install
// placed there is gone, naming the path and the repair.
func TestClaudePluginsDoctorChecksTheCallmeterBinary(t *testing.T) {
	for _, state := range []string{"present", "missing", "not executable"} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			store := filepath.Join(home, ".claude")
			root := filepath.Join(home, "src")
			callmeterHome := filepath.Join(home, "callmeter-state")
			env := &paths.MapEnv{Values: map[string]string{"CALLMETER_HOME": callmeterHome}}
			for _, dir := range []string{
				filepath.Join(root, "callmeter", "plugins", "callmeter"),
				filepath.Join(home, ".local", "share", "callmeter-dev", "plugins", "callmeter"),
			} {
				if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(dir, ".claude-plugin", "plugin.json"),
					[]byte(`{"version":"0.3.0"}`),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(callmeterHome, "bin", "0.3.0", "callmeter")
			if state != "missing" {
				if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				if state == "not executable" {
					if err := os.Chmod(binary, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := os.MkdirAll(filepath.Join(store, "plugins"), 0o700); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"enabledPlugins": map[string]bool{"callmeter@callmeter-dev": true}})
			if err := os.WriteFile(filepath.Join(store, "settings.json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			writeDoctorInstalledPlugins(t, store, "callmeter@callmeter-dev")
			writeDoctorCache(t, store, "callmeter@callmeter-dev", "0.3.0", map[string]string{
				filepath.Join(".claude-plugin", "plugin.json"): `{"version":"0.3.0"}`,
			})
			build := installer.ClaudePluginBuild{
				Version: "0.80.0-alpha", Home: home, CheckoutRoot: root,
				Plugins: []pfmconfig.ClaudePlugin{{ID: "callmeter"}},
			}
			want, failures := "doctor: claude_plugins ok\n", 0
			switch state {
			case "missing":
				want, failures = "doctor: claude_plugins FAIL plugin callmeter@callmeter-dev binary "+binary+
					" missing: its hooks would download a release binary, which an unreleased version fails"+
					" — run pfm install --yes\n", 1
			case "not executable":
				want, failures = "doctor: claude_plugins FAIL plugin callmeter@callmeter-dev binary "+binary+
					" is not an executable file — run pfm install --yes\n", 1
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudePluginsDoctor(&out, store, env, build, tally)
			if out.String() != want || tally.warnings != 0 || tally.failures != failures {
				t.Fatalf("got %q tally=%+v\nwant %q failures=%d", out.String(), tally, want, failures)
			}
		})
	}
}
