package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// pluginLogsFixture is one alpha host: a store and two account config dirs
// (the first linking its settings.json to the store's), a source clone whose
// marketplace lists alpha-plug with its checkout at version 1.2.0, and the
// store's install record of alpha-plug's -dev copy.
type pluginLogsFixture struct {
	home, store, linked, own, clone, checkout, folder string
}

func writePluginLogsFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pluginLogSettings(t *testing.T, path, value string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"env": map[string]string{installer.PluginLogDirEnv: value}})
	if err != nil {
		t.Fatal(err)
	}
	writePluginLogsFile(t, path, string(raw))
}

func pluginLogsInstalled(t *testing.T, store string, versions ...any) {
	t.Helper()
	entries := make([]any, 0, len(versions))
	for _, version := range versions {
		entries = append(
			entries,
			map[string]any{"installPath": filepath.Join(store, "plugins", "cache"), "version": version},
		)
	}
	raw, err := json.Marshal(
		map[string]any{"version": 2, "plugins": map[string]any{"alpha-plug@alpha-plug-dev": entries}},
	)
	if err != nil {
		t.Fatal(err)
	}
	writePluginLogsFile(t, filepath.Join(store, "plugins", "installed_plugins.json"), string(raw))
}

// pluginLogsMarketplace rewrites the source clone's marketplace to list names.
func pluginLogsMarketplace(t *testing.T, fixture pluginLogsFixture, names ...string) {
	t.Helper()
	plugins := make([]any, 0, len(names))
	for _, name := range names {
		plugins = append(plugins, map[string]string{"name": name, "source": "./plugins/" + name + "/plugins/" + name})
	}
	raw, err := json.Marshal(map[string]any{"name": "professor", "plugins": plugins})
	if err != nil {
		t.Fatal(err)
	}
	writePluginLogsFile(t, filepath.Join(fixture.clone, ".claude-plugin", "marketplace.json"), string(raw))
}

func newPluginLogsFixture(t *testing.T) pluginLogsFixture {
	t.Helper()
	home := t.TempDir()
	fixture := pluginLogsFixture{
		home:   home,
		store:  filepath.Join(home, ".claude"),
		linked: filepath.Join(home, ".cc", "1"),
		own:    filepath.Join(home, ".cc", "2"),
		clone:  filepath.Join(home, "clone"),
		folder: filepath.Join(home, "plugin-logs"),
	}
	fixture.checkout = filepath.Join(fixture.clone, "plugins", "alpha-plug", "plugins", "alpha-plug")
	writePluginLogsFile(t, filepath.Join(fixture.clone, ".claude-plugin", "marketplace.json"),
		`{"name":"professor","plugins":[{"name":"alpha-plug","source":"./plugins/alpha-plug/plugins/alpha-plug"}]}`)
	writePluginLogsFile(t, filepath.Join(fixture.checkout, ".claude-plugin", "plugin.json"),
		`{"name":"alpha-plug","version":"1.2.0"}`)
	pluginLogSettings(t, filepath.Join(fixture.store, "settings.json"), fixture.folder)
	pluginLogSettings(t, filepath.Join(fixture.own, "settings.json"), fixture.folder)
	if err := os.MkdirAll(fixture.linked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(fixture.store, "settings.json"),
		filepath.Join(fixture.linked, "settings.json"),
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fixture.folder, 0o700); err != nil {
		t.Fatal(err)
	}
	pluginLogsInstalled(t, fixture.store, "1.2.0")
	return fixture
}

// TestPluginLogsDoctor: on an -alpha build the plugin_logs rows print ok only
// for a host whose every config dir sets one absolute, usable folder, with no
// fallback rows and every -dev install at its checkout's version; each broken
// state prints its own line, a failed read a failure, never a missing value;
// a release build prints one untallied does-not-apply line.
func TestPluginLogsDoctor(t *testing.T) {
	var parseErr any
	jsonErr := json.Unmarshal([]byte("{"), &parseErr).Error()
	for _, test := range []struct {
		name               string
		version            string
		plugins            []string
		env                func(fixture pluginLogsFixture) map[string]string
		mutate             func(t *testing.T, fixture pluginLogsFixture)
		want               func(fixture pluginLogsFixture) string
		warnings, failures int
	}{
		{
			name:    "a release build does not apply",
			version: "0.82.0",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				writePluginLogsFile(t, filepath.Join(fixture.own, "settings.json"), "{")
			},
			want: func(pluginLogsFixture) string {
				return "doctor: plugin_logs does not apply: pfm 0.82.0 is not an -alpha build\n"
			},
		},
		{
			name: "a healthy host is ok",
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs ok: PLUGIN_LOG_DIR=" + fixture.folder + " in every config dir\n"
			},
		},
		{
			name: "missing from one config dir's own settings.json",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				writePluginLogsFile(t, filepath.Join(fixture.own, "settings.json"), `{"env":{"OTHER":"x"}}`)
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs PLUGIN_LOG_DIR is missing for config dir " + fixture.own + ": " +
					filepath.Join(fixture.own, "settings.json") +
					" sets no env.PLUGIN_LOG_DIR — set it to the shared log folder\n"
			},
			warnings: 1,
		},
		{
			name: "missing because a config dir has no settings.json",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				if err := os.Remove(filepath.Join(fixture.own, "settings.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs PLUGIN_LOG_DIR is missing for config dir " + fixture.own + ": no " +
					filepath.Join(fixture.own, "settings.json") +
					" — set env.PLUGIN_LOG_DIR in it to the shared log folder\n"
			},
			warnings: 1,
		},
		{
			name: "values differ across config dirs",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				other := filepath.Join(fixture.home, "other-logs")
				if err := os.MkdirAll(other, 0o700); err != nil {
					t.Fatal(err)
				}
				pluginLogSettings(t, filepath.Join(fixture.own, "settings.json"), other)
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs PLUGIN_LOG_DIR differs across config dirs: " +
					fixture.store + "=" + fixture.folder + " " + fixture.linked + "=" + fixture.folder + " " +
					fixture.own + "=" + filepath.Join(fixture.home, "other-logs") +
					" — set one folder in all of them\n"
			},
			warnings: 1,
		},
		{
			name: "two spellings of one folder are one value",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				pluginLogSettings(t, filepath.Join(fixture.own, "settings.json"), fixture.folder+"/")
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs ok: PLUGIN_LOG_DIR=" + fixture.folder + " in every config dir\n"
			},
		},
		{
			name: "file-derived text prints escaped, never a forged line",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				forged := "/absent\ndoctor: plugin_logs ok"
				pluginLogSettings(t, filepath.Join(fixture.store, "settings.json"), forged)
				pluginLogSettings(t, filepath.Join(fixture.own, "settings.json"), forged)
				pluginLogsInstalled(t, fixture.store, "1.1.0\ndoctor: plugin_logs ok")
			},
			want: func(fixture pluginLogsFixture) string {
				return `doctor: plugin_logs no log folder at /absent\ndoctor: plugin_logs ok — create it: mkdir -p ` +
					`/absent\ndoctor: plugin_logs ok` + "\n" +
					`doctor: plugin_logs plugin alpha-plug@alpha-plug-dev is stale: installed 1.1.0\ndoctor: plugin_logs ok,` +
					" checkout 1.2.0 — refresh the -dev copy from " + fixture.checkout + "\n"
			},
			warnings: 2,
		},
		{
			name: "an invalid shared settings.json is a failure for every dir on it",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				writePluginLogsFile(t, filepath.Join(fixture.store, "settings.json"), "{")
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs could not read PLUGIN_LOG_DIR for config dir " + fixture.store + ", " +
					fixture.linked + ": parse " + filepath.Join(fixture.store, "settings.json") + ": " + jsonErr + "\n"
			},
			failures: 1,
		},
		{
			name: "a relative value is not probed",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				pluginLogSettings(t, filepath.Join(fixture.store, "settings.json"), "plugin-logs")
				pluginLogSettings(t, filepath.Join(fixture.own, "settings.json"), "plugin-logs")
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs PLUGIN_LOG_DIR \"plugin-logs\" for config dir " + fixture.store + ", " +
					fixture.linked + ", " + fixture.own +
					" is not an absolute path — plugins resolve it from wherever they run\n"
			},
			warnings: 1,
		},
		{
			name: "no log folder",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				if err := os.Remove(fixture.folder); err != nil {
					t.Fatal(err)
				}
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs no log folder at " + fixture.folder + " — create it: mkdir -p " +
					fixture.folder + "\n"
			},
			warnings: 1,
		},
		{
			name: "the log folder is a file",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				if err := os.Remove(fixture.folder); err != nil {
					t.Fatal(err)
				}
				writePluginLogsFile(t, fixture.folder, "x")
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs the log folder " + fixture.folder +
					" is not a directory — replace it with one\n"
			},
			failures: 1,
		},
		{
			name: "rows stranded in a fallback while the variable is set",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				logs := filepath.Join(fixture.home, ".local", "state", "alpha-plug", "logs")
				writePluginLogsFile(t, filepath.Join(logs, "errors.jsonl"), "{\"a\":1}\n\n{\"a\":2}\n")
				writePluginLogsFile(t, filepath.Join(logs, "info.jsonl"), "{\"a\":3}")
				writePluginLogsFile(t, filepath.Join(logs, "errors.jsonl.1"), "{\"a\":0}\n")
				writePluginLogsFile(t, filepath.Join(logs, "notes.txt"), "not a row\n")
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs plugin alpha-plug: 4 rows stranded in its fallback " +
					filepath.Join(fixture.home, ".local", "state", "alpha-plug", "logs") +
					" while PLUGIN_LOG_DIR is set — move them to the log folder\n"
			},
			warnings: 1,
		},
		{
			name: "callmeter's fallback follows XDG_STATE_HOME",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				pluginLogsMarketplace(t, fixture, "alpha-plug", "callmeter")
				writePluginLogsFile(t, filepath.Join(fixture.home, "xdg", "callmeter", "logs", "errors.jsonl"), "{}\n")
			},
			env: func(fixture pluginLogsFixture) map[string]string {
				return map[string]string{"XDG_STATE_HOME": filepath.Join(fixture.home, "xdg")}
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs plugin callmeter: 1 rows stranded in its fallback " +
					filepath.Join(fixture.home, "xdg", "callmeter", "logs") +
					" while PLUGIN_LOG_DIR is set — move them to the log folder\n"
			},
			warnings: 1,
		},
		{
			name: "callmeter's fallback follows CALLMETER_HOME",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				pluginLogsMarketplace(t, fixture, "alpha-plug", "callmeter")
				writePluginLogsFile(t, filepath.Join(fixture.home, "meter", "logs", "info.jsonl"), "{}\n")
			},
			env: func(fixture pluginLogsFixture) map[string]string {
				return map[string]string{
					"CALLMETER_HOME": filepath.Join(fixture.home, "meter"),
					"XDG_STATE_HOME": filepath.Join(fixture.home, "xdg"),
				}
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs plugin callmeter: 1 rows stranded in its fallback " +
					filepath.Join(fixture.home, "meter", "logs") +
					" while PLUGIN_LOG_DIR is set — move them to the log folder\n"
			},
			warnings: 1,
		},
		{
			name: "a fallback log that is not a regular file is a failure to count",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				logs := filepath.Join(fixture.home, ".local", "state", "alpha-plug", "logs")
				if err := os.MkdirAll(logs, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(os.DevNull, filepath.Join(logs, "errors.jsonl")); err != nil {
					t.Fatal(err)
				}
			},
			want: func(fixture pluginLogsFixture) string {
				logs := filepath.Join(fixture.home, ".local", "state", "alpha-plug", "logs")
				return "doctor: plugin_logs plugin alpha-plug: could not count the rows in its fallback " + logs + ": " +
					filepath.Join(logs, "errors.jsonl") + " is not a regular file\n"
			},
			failures: 1,
		},
		{
			name: "fallback rows are not judged while the variable is unset",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				for _, path := range []string{fixture.store, fixture.own} {
					writePluginLogsFile(t, filepath.Join(path, "settings.json"), "{}")
				}
				writePluginLogsFile(t,
					filepath.Join(fixture.home, ".local", "state", "alpha-plug", "logs", "errors.jsonl"), "{}\n")
			},
			want: func(fixture pluginLogsFixture) string {
				line := func(dirs, settings string) string {
					return "doctor: plugin_logs PLUGIN_LOG_DIR is missing for config dir " + dirs + ": " + settings +
						" sets no env.PLUGIN_LOG_DIR — set it to the shared log folder\n"
				}
				return line(fixture.store+", "+fixture.linked, filepath.Join(fixture.store, "settings.json")) +
					line(fixture.own, filepath.Join(fixture.own, "settings.json"))
			},
			warnings: 2,
		},
		{
			name: "an unreadable marketplace list is a failure to look, not a clean fallback",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				writePluginLogsFile(t, filepath.Join(fixture.clone, ".claude-plugin", "marketplace.json"), "{")
			},
			want: func(fixture pluginLogsFixture) string {
				listErr := "parse the marketplace plugin list " +
					filepath.Join(fixture.clone, ".claude-plugin", "marketplace.json") + ": " + jsonErr
				return "doctor: plugin_logs fallback logs not checked: " + listErr + "\n" +
					"doctor: plugin_logs -dev versions compared for claude.plugins only: " + listErr + "\n"
			},
			failures: 1,
		},
		{
			name: "a stale -dev install names both versions",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				pluginLogsInstalled(t, fixture.store, "1.1.0")
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs plugin alpha-plug@alpha-plug-dev is stale: installed 1.1.0, checkout 1.2.0" +
					" — refresh the -dev copy from " + fixture.checkout + "\n"
			},
			warnings: 1,
		},
		{
			name: "a -dev copy with no install record is not compared",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				if err := os.Remove(filepath.Join(fixture.store, "plugins", "installed_plugins.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs plugin alpha-plug@alpha-plug-dev has no install record naming a version in " +
					fixture.store + ", its version not compared\n"
			},
		},
		{
			name: "a record naming no version string is not compared, never an unreadable file",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				pluginLogsInstalled(t, fixture.store, 7)
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs plugin alpha-plug@alpha-plug-dev has no install record naming a version in " +
					fixture.store + ", its version not compared\n"
			},
		},
		{
			name:    "a listed plugin with no -dev checkout is not compared, so not ok",
			plugins: []string{"alpha-plug", "gone-plug"},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs plugin gone-plug has no -dev checkout to compare (no checkout at " +
					filepath.Join(fixture.clone, "plugins", "gone-plug", "plugins", "gone-plug", ".claude-plugin",
						"plugin.json") +
					" (the source clone's plugins/; claude.pluginCheckoutRoot unset)), its version not compared\n"
			},
		},
		{
			name: "an unreadable install record is a failure",
			mutate: func(t *testing.T, fixture pluginLogsFixture) {
				t.Helper()
				writePluginLogsFile(t, filepath.Join(fixture.store, "plugins", "installed_plugins.json"), "{")
			},
			want: func(fixture pluginLogsFixture) string {
				return "doctor: plugin_logs -dev versions not compared: parse " +
					filepath.Join(fixture.store, "plugins", "installed_plugins.json") + ": " + jsonErr + "\n"
			},
			failures: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPluginLogsFixture(t)
			if test.mutate != nil {
				test.mutate(t, fixture)
			}
			version := test.version
			if version == "" {
				version = "0.80.0-alpha"
			}
			build := installer.ClaudePluginBuild{
				Version:    version,
				Home:       fixture.home,
				SourceRepo: fixture.clone,
				Plugins:    []pfmconfig.ClaudePlugin{{ID: "alpha-plug"}},
			}
			if test.plugins != nil {
				build.Plugins = nil
				for _, name := range test.plugins {
					build.Plugins = append(build.Plugins, pfmconfig.ClaudePlugin{ID: name})
				}
			}
			var stdout bytes.Buffer
			tally := &doctorTally{}
			env := &paths.MapEnv{}
			if test.env != nil {
				env.Values = test.env(fixture)
			}
			printPluginLogsDoctor(&stdout, []string{fixture.store, fixture.linked, fixture.own}, fixture.store, build,
				env, unix.Access, tally)
			if got, want := stdout.String(), test.want(fixture); got != want {
				t.Fatalf("output:\n%s\nwant:\n%s", got, want)
			}
			if tally.warnings != test.warnings || tally.failures != test.failures {
				t.Fatalf("tally warnings=%d failures=%d, want %d and %d", tally.warnings, tally.failures,
					test.warnings, test.failures)
			}
		})
	}
}

// TestPluginLogFolderGap: a folder the kernel refuses rwx on is unusable, a
// failure distinct from one not there yet; a usable folder has no gap.
func TestPluginLogFolderGap(t *testing.T) {
	folder := t.TempDir()
	refuse := func(string, uint32) error { return unix.EACCES }
	gap, failure := pluginLogFolderGap(folder, refuse)
	if want := "the log folder " + folder + " is unreadable or unwritable: permission denied — grant this user rwx on it"; gap != want ||
		!failure {
		t.Fatalf("refused folder gap=%q failure=%v, want %q and a failure", gap, failure, want)
	}
	if gap, failure := pluginLogFolderGap(folder, unix.Access); gap != "" || failure {
		t.Fatalf("usable folder gap=%q failure=%v, want none", gap, failure)
	}
	link := filepath.Join(folder, "link")
	if err := os.Symlink(filepath.Join(folder, "gone"), link); err != nil {
		t.Fatal(err)
	}
	if gap, failure := pluginLogFolderGap(link, refuse); gap != "the log folder "+link+
		" is a link to a missing target — point it at a directory" || !failure {
		t.Fatalf("dangling link gap=%q failure=%v, want a dangling-link failure", gap, failure)
	}
	absent := filepath.Join(folder, "absent")
	if gap, failure := pluginLogFolderGap(
		absent,
		refuse,
	); !strings.HasPrefix(gap, "no log folder at "+absent) ||
		failure {
		t.Fatalf("absent folder gap=%q failure=%v, want a no-log-folder warning", gap, failure)
	}
}
