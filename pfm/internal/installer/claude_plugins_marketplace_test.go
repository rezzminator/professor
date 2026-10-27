package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestClaudePluginMarketplaceGaps: a target whose marketplace is registered
// from any source but the one pfm install adds is named; a matching source, an
// unregistered marketplace and a store with no record name nothing.
func TestClaudePluginMarketplaceGaps(t *testing.T) {
	local := ClaudePluginTarget{Name: "p", ID: "p@p-dev", Source: "/share/p-dev", Local: true}
	github := ClaudePluginTarget{Name: "q", ID: "q@q", Source: "owner/q"}
	sourceless := ClaudePluginTarget{Name: "r", ID: "r@known", MarketplaceName: "known"}
	byURL := ClaudePluginTarget{
		Name: "s", ID: "s@byurl", Source: "https://example.com/byurl.git", MarketplaceName: "byurl",
	}
	pinned := ClaudePluginTarget{Name: "h", ID: "h@h", Source: "owner/h#v1", MarketplaceName: "h"}
	relative := ClaudePluginTarget{Name: "m", ID: "m@mk", Source: "./mk", MarketplaceName: "mk"}
	home := ClaudePluginTarget{Name: "n", ID: "n@mk2", Source: "~/mk2", MarketplaceName: "mk2"}
	targets := []ClaudePluginTarget{local, github, sourceless, byURL, pinned, relative, home}
	repair := "claude plugin marketplace remove "
	for _, test := range []struct {
		name    string
		sources map[string]any
		want    []ClaudePluginGap
	}{
		{"matching", map[string]any{
			"p-dev": map[string]any{"source": "directory", "path": "/share/p-dev"},
			"q":     map[string]any{"source": "github", "repo": "owner/q"},
		}, nil},
		{"unregistered", map[string]any{"other": map[string]any{"source": "github", "repo": "x/other"}}, nil},
		{"sourceless full id", map[string]any{
			"known": map[string]any{"source": "github", "repo": "someone/known"},
		}, nil},
		{"matching git url", map[string]any{
			"byurl": map[string]any{"source": "git", "url": "https://example.com/byurl.git"},
		}, nil},
		{"matching url record", map[string]any{
			"byurl": map[string]any{"source": "url", "url": "https://example.com/byurl.git"},
		}, nil},
		{"foreign url record", map[string]any{
			"byurl": map[string]any{"source": "url", "url": "https://example.com/other.git"},
		}, []ClaudePluginGap{{
			"plugin s@byurl marketplace byurl in {file} points at url https://example.com/other.git, " +
				"want https://example.com/byurl.git",
			repair + "byurl, then run pfm install --yes",
		}}},
		{"directory elsewhere", map[string]any{
			"p-dev": map[string]any{"source": "directory", "path": "/elsewhere/p-dev"},
		}, []ClaudePluginGap{{
			"plugin p@p-dev marketplace p-dev in {file} points at /elsewhere/p-dev, want /share/p-dev",
			// pfm install re-points its own -dev marketplace: no remove.
			"run pfm install --yes",
		}}},
		{"github for a local target", map[string]any{
			"p-dev": map[string]any{"source": "github", "repo": "owner/p"},
		}, []ClaudePluginGap{{
			"plugin p@p-dev marketplace p-dev in {file} points at owner/p, want /share/p-dev",
			"run pfm install --yes",
		}}},
		{"shorthand with a ref", map[string]any{
			"h": map[string]any{"source": "github", "repo": "owner/h"},
		}, nil},
		{"shorthand with a ref elsewhere", map[string]any{
			"h": map[string]any{"source": "github", "repo": "owner/other"},
		}, []ClaudePluginGap{{
			"plugin h@h marketplace h in {file} points at owner/other, want owner/h#v1",
			repair + "h, then run pfm install --yes",
		}}},
		{"directory with a trailing slash", map[string]any{
			"p-dev": map[string]any{"source": "directory", "path": "/share/p-dev/"},
		}, nil},
		{"relative and home paths are not compared", map[string]any{
			"mk":  map[string]any{"source": "directory", "path": "/abs/mk"},
			"mk2": map[string]any{"source": "directory", "path": "/abs/mk2"},
		}, nil},
		{"relative path registered from github", map[string]any{
			"mk": map[string]any{"source": "github", "repo": "owner/mk"},
		}, []ClaudePluginGap{{
			"plugin m@mk marketplace mk in {file} points at owner/mk, want ./mk",
			repair + "mk, then run pfm install --yes",
		}}},
		{"github record for a url is not compared", map[string]any{
			"byurl": map[string]any{"source": "github", "repo": "owner/byurl"},
		}, nil},
		{"directory for a url", map[string]any{
			"byurl": map[string]any{"source": "directory", "path": "/abs/byurl"},
		}, []ClaudePluginGap{{
			"plugin s@byurl marketplace byurl in {file} points at /abs/byurl, want https://example.com/byurl.git",
			repair + "byurl, then run pfm install --yes",
		}}},
		{"control characters escaped", map[string]any{
			"byurl": map[string]any{"source": "url", "url": "https://example.com/o\ndoctor: failures=9"},
		}, []ClaudePluginGap{{
			`plugin s@byurl marketplace byurl in {file} points at url https://example.com/o\ndoctor: failures=9, ` +
				"want https://example.com/byurl.git",
			repair + "byurl, then run pfm install --yes",
		}}},
		{"git url", map[string]any{
			"q": map[string]any{"source": "git", "url": "https://example.com/q.git"},
		}, []ClaudePluginGap{{
			"plugin q@q marketplace q in {file} points at git https://example.com/q.git, want owner/q",
			repair + "q, then run pfm install --yes",
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := t.TempDir()
			file := filepath.Join(store, "plugins", "known_marketplaces.json")
			known := map[string]any{}
			for name, source := range test.sources {
				known[name] = map[string]any{"source": source}
			}
			raw, _ := json.Marshal(known)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			gaps, err := ClaudePluginMarketplaceGaps(store, targets)
			for index := range test.want {
				test.want[index].Problem = strings.ReplaceAll(test.want[index].Problem, "{file}", file)
			}
			if err != nil || fmt.Sprint(gaps) != fmt.Sprint(test.want) {
				t.Fatalf("got %v, %v want %v", gaps, err, test.want)
			}
		})
	}
	t.Run("no record", func(t *testing.T) {
		gaps, err := ClaudePluginMarketplaceGaps(t.TempDir(), []ClaudePluginTarget{local, github})
		if err != nil || gaps != nil {
			t.Fatalf("got %v, %v want no gap and no error", gaps, err)
		}
	})
	t.Run("unparsable record", func(t *testing.T) {
		store := t.TempDir()
		file := filepath.Join(store, "plugins", "known_marketplaces.json")
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ClaudePluginMarketplaceGaps(store, []ClaudePluginTarget{local}); err == nil ||
			!strings.Contains(err.Error(), file) {
			t.Fatalf("got %v, want an error naming %s", err, file)
		}
	})
}

// writeMarketplaceClone lays out a source clone: its .claude-plugin/marketplace.json
// listing names in order, and a checkout under plugins/ for each of withCheckout.
func writeMarketplaceClone(t *testing.T, names, withCheckout []string) string {
	t.Helper()
	clone := t.TempDir()
	entries := make([]any, 0, len(names))
	for _, name := range names {
		entries = append(entries, map[string]any{"name": name, "source": "./plugins/" + name + "/plugins/" + name})
	}
	raw, err := json.Marshal(map[string]any{"name": "professor", "plugins": entries})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(clone, ".claude-plugin", "marketplace.json"), string(raw))
	for _, name := range withCheckout {
		writePluginCheckout(t, filepath.Join(clone, "plugins"), name)
	}
	return clone
}

func bareList(names ...string) []pfmconfig.ClaudePlugin {
	list := make([]pfmconfig.ClaudePlugin, 0, len(names))
	for _, name := range names {
		list = append(list, pfmconfig.ClaudePlugin{ID: name})
	}
	return list
}

// TestClaudePluginBuildWithMarketplacePlugins: an -alpha build reads the
// source clone's marketplace: unset claude.plugins becomes every plugin it
// lists, an explicit list stays and the rest are Unlisted; a release and a
// build with no clone are untouched; a list that cannot be read is an error
// beside the unchanged build, never an empty list.
func TestClaudePluginBuildWithMarketplacePlugins(t *testing.T) {
	names := []string{"forward-compaction", "engine-sync", "buddy", "auto-effort-control", "example-plugin"}
	clone := writeMarketplaceClone(t, names, nil)
	manifest := func(t *testing.T, content string) string {
		dir := t.TempDir()
		writeFixture(t, filepath.Join(dir, ".claude-plugin", "marketplace.json"), content)
		return dir
	}
	defaults := pfmconfig.DefaultClaudePlugins()
	unresolved := errors.New("recorded source repository /gone is not a directory")
	for _, test := range []struct {
		name         string
		build        func(t *testing.T) ClaudePluginBuild
		wantPlugins  []pfmconfig.ClaudePlugin
		wantUnlisted []string
		wantErr      string
	}{
		{"alpha default is every marketplace plugin", func(*testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{Version: alphaVersion, SourceRepo: clone, Plugins: defaults, PluginsDefault: true}
		}, bareList(names...), nil, ""},
		{"an explicit list stays and the rest are unlisted", func(*testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{Version: alphaVersion, SourceRepo: clone, Plugins: []pfmconfig.ClaudePlugin{
				{ID: "forward-compaction"}, {ID: "engine-sync@professor"}, {ID: "typesafe@typesafe-ai"},
			}}
		}, []pfmconfig.ClaudePlugin{
			{ID: "forward-compaction"}, {ID: "engine-sync@professor"}, {ID: "typesafe@typesafe-ai"},
		}, []string{"buddy", "auto-effort-control", "example-plugin"}, ""},
		{"a release reads nothing", func(*testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{Version: "0.80.0", SourceRepo: clone, Plugins: defaults, PluginsDefault: true}
		}, defaults, nil, ""},
		{"no source clone", func(*testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{Version: alphaVersion, Plugins: defaults, PluginsDefault: true}
		}, defaults, nil, ""},
		{"an explicit list beside a clone without a marketplace", func(t *testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{Version: alphaVersion, SourceRepo: t.TempDir(), Plugins: bareList("buddy")}
		}, bareList("buddy"), nil, ""},
		{"a default list beside a clone without a marketplace", func(t *testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{Version: alphaVersion, SourceRepo: t.TempDir(), Plugins: defaults, PluginsDefault: true}
		}, defaults, nil, "read the marketplace plugin list "},
		{"an unresolved source clone", func(*testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{Version: alphaVersion, SourceRepoErr: unresolved, Plugins: defaults, PluginsDefault: true}
		}, defaults, nil, "source clone unresolved: " + unresolved.Error()},
		{"garbage", func(t *testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{Version: alphaVersion, SourceRepo: manifest(t, "{"), Plugins: defaults, PluginsDefault: true}
		}, defaults, nil, "parse the marketplace plugin list "},
		{"no plugins", func(t *testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{
				Version: alphaVersion, SourceRepo: manifest(t, `{"name":"professor","plugins":[]}`), Plugins: defaults,
			}
		}, defaults, nil, "lists no plugins"},
		{"a name that is not a plugin name", func(t *testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{
				Version: alphaVersion, SourceRepo: manifest(t, `{"plugins":[{"name":"../x","source":"./x"}]}`),
				Plugins: defaults, PluginsDefault: true,
			}
		}, defaults, nil, `plugins[0] name "../x" is not a plugin name`},
		{"a name listed twice", func(t *testing.T) ClaudePluginBuild {
			return ClaudePluginBuild{
				Version:    alphaVersion,
				SourceRepo: manifest(t, `{"plugins":[{"name":"a","source":"./a"},{"name":"a","source":"./b"}]}`),
				Plugins:    defaults, PluginsDefault: true,
			}
		}, defaults, nil, "lists a twice"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.build(t).WithMarketplacePlugins()
			if test.wantErr == "" && err != nil ||
				test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("err = %v, want %q", err, test.wantErr)
			}
			if !reflect.DeepEqual(got.Plugins, test.wantPlugins) ||
				!reflect.DeepEqual(got.Unlisted, test.wantUnlisted) {
				t.Fatalf(
					"plugins %v unlisted %v, want %v and %v",
					got.Plugins,
					got.Unlisted,
					test.wantPlugins,
					test.wantUnlisted,
				)
			}
		})
	}
}

// simulateClaude plays the claude CLI's own writes for the fake runner in the
// account dir it runs on: an install enables the id in settings.json (through
// its link) and records it installed, a directory marketplace's plugin with a
// snapshot of its copy at its version; a directory marketplace add records the
// marketplace under the name its manifest gives, re-pointing a known one.
func simulateClaude(t *testing.T, runner *pluginRunner) {
	t.Helper()
	readJSON := func(path string) map[string]any {
		document := map[string]any{}
		if raw, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Errorf("simulated claude: parse %s: %v", path, err)
			}
		}
		return document
	}
	writeJSON := func(path string, document map[string]any) {
		raw, err := json.Marshal(document)
		if err != nil {
			t.Errorf("simulated claude: %v", err)
			return
		}
		writeFixture(t, path, string(raw))
	}
	runner.onRun = func(configDir, argv string) {
		fields := strings.Fields(argv)
		switch {
		case len(fields) == 4 && fields[0] == "plugin" && fields[1] == "install":
			settings := filepath.Join(configDir, "settings.json")
			document := readJSON(settings)
			enabled, _ := document["enabledPlugins"].(map[string]any)
			if enabled == nil {
				enabled = map[string]any{}
				document["enabledPlugins"] = enabled
			}
			enabled[fields[2]] = true
			writeJSON(settings, document)
			record := filepath.Join(configDir, "plugins", "installed_plugins.json")
			installed := readJSON(record)
			plugins, _ := installed["plugins"].(map[string]any)
			if plugins == nil {
				plugins = map[string]any{}
				installed["plugins"] = plugins
			}
			installPath := filepath.Join(configDir, "plugins", "cache", fields[2])
			if err := os.MkdirAll(installPath, 0o700); err != nil {
				t.Errorf("simulated claude: %v", err)
			}
			entry := map[string]any{"scope": "user", "installPath": installPath}
			// A directory marketplace's plugin is snapshotted at its plugin.json version.
			name, marketplace, _ := strings.Cut(fields[2], "@")
			known := readJSON(filepath.Join(configDir, "plugins", "known_marketplaces.json"))
			listing, _ := known[marketplace].(map[string]any)
			source, _ := listing["source"].(map[string]any)
			if dir, _ := source["path"].(string); source["source"] == "directory" && dir != "" {
				copyDir := filepath.Join(dir, "plugins", name)
				version, _ := readJSON(filepath.Join(copyDir, ".claude-plugin", "plugin.json"))["version"].(string)
				entry["version"] = version
				if err := mirrorPluginTree(copyDir, installPath); err != nil {
					t.Errorf("simulated claude: %v", err)
				}
			}
			plugins[fields[2]] = []any{entry}
			writeJSON(record, installed)
		case len(fields) == 4 && strings.Join(fields[:3], " ") == "plugin marketplace add" && filepath.IsAbs(fields[3]):
			manifest := readJSON(filepath.Join(fields[3], ".claude-plugin", "marketplace.json"))
			name, _ := manifest["name"].(string)
			path := filepath.Join(configDir, "plugins", "known_marketplaces.json")
			known := readJSON(path)
			known[name] = map[string]any{"source": map[string]any{"source": "directory", "path": fields[3]}}
			writeJSON(path, known)
		}
	}
}

// TestEnsureClaudePluginsAlphaMarketplacePlugins: an -alpha install puts every
// plugin the source clone's marketplace lists on its -dev copy from the
// checkout. claude.plugins is the enabled set: a listed plugin is enabled under
// its -dev id, its professor copy disabled and its options carried; an
// unlisted one is installed (a foreign -dev marketplace re-pointed, never
// removed) and every copy kept disabled. Unset, every plugin is enabled. An
// existing -dev options entry is never overwritten.
func TestEnsureClaudePluginsAlphaMarketplacePlugins(t *testing.T) {
	names := []string{"forward-compaction", "engine-sync", "buddy", "auto-effort-control"}
	for _, test := range []struct {
		name        string
		list        []pfmconfig.ClaudePlugin
		listDefault bool
		preview     bool
		wantCalls   func(marketplace func(string) string) []string
		wantEnabled map[string]any
	}{
		{
			"claude.plugins is the enabled set", bareList("forward-compaction", "engine-sync"), false, false,
			func(marketplace func(string) string) []string {
				return []string{
					"plugin marketplace add " + marketplace("engine-sync"), "plugin install engine-sync@engine-sync-dev -y",
					"plugin marketplace add " + marketplace("buddy"),
					"plugin marketplace add " + marketplace("auto-effort-control"), "plugin install auto-effort-control@auto-effort-control-dev -y",
				}
			},
			map[string]any{
				"forward-compaction@forward-compaction-dev":   true,
				"engine-sync@professor":                       false,
				"engine-sync@engine-sync-dev":                 true,
				"buddy@buddy-dev":                             false,
				"auto-effort-control@auto-effort-control-dev": false,
			},
		},
		{
			"unset enables every marketplace plugin", pfmconfig.DefaultClaudePlugins(), true, false,
			func(marketplace func(string) string) []string {
				return []string{
					"plugin marketplace add " + marketplace("engine-sync"), "plugin install engine-sync@engine-sync-dev -y",
					"plugin marketplace add " + marketplace("buddy"), "plugin install buddy@buddy-dev -y",
					"plugin marketplace add " + marketplace("auto-effort-control"), "plugin install auto-effort-control@auto-effort-control-dev -y",
				}
			},
			map[string]any{
				"forward-compaction@forward-compaction-dev":   true,
				"engine-sync@professor":                       false,
				"engine-sync@engine-sync-dev":                 true,
				"buddy@buddy-dev":                             true,
				"auto-effort-control@auto-effort-control-dev": true,
			},
		},
		// The install enables an install-only plugin and the edit after it disables
		// it: a preview, which cannot read the installed state, names both.
		{"a preview names the disable after an install", bareList("forward-compaction"), false, true, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			inst, runner, out, home, first := alphaPluginEngine(t, "")
			inst.apply = !test.preview
			inst.options.SourceRepo = writeMarketplaceClone(t, names, names)
			inst.options.ClaudePlugins = test.list
			inst.options.ClaudePluginsDefault = test.listDefault
			simulateClaude(t, runner)
			marketplace := func(name string) string { return filepath.Join(home, ".local", "share", name+"-dev") }
			// The store's settings.json, linked from the account dir.
			store := filepath.Join(home, ".claude", "settings.json")
			writeFixture(t, store, `{"enabledPlugins":{"forward-compaction@forward-compaction-dev":true,`+
				`"engine-sync@professor":true,"buddy@buddy-dev":false},"pluginConfigs":{`+
				`"forward-compaction@forward-compaction-dev":{"options":{"ceilingNative":true}},`+
				`"forward-compaction@professor":{"options":{"ceilingNative":false}},`+
				`"engine-sync@professor":{"options":{"y":2}},"buddy@buddy-dev":{"options":{"x":1}}}}`)
			if err := os.Symlink(store, filepath.Join(first, "settings.json")); err != nil {
				t.Fatal(err)
			}
			writeInstalledPlugins(
				t,
				first,
				"forward-compaction@forward-compaction-dev",
				"engine-sync@professor",
				"buddy@buddy-dev",
			)
			known, err := json.Marshal(map[string]any{
				"forward-compaction-dev": map[string]any{"source": map[string]any{
					"source": "directory", "path": marketplace("forward-compaction"),
				}},
				"buddy-dev": map[string]any{"source": map[string]any{
					"source": "directory", "path": filepath.Join(home, ".config", "buddy", "dev-marketplace"),
				}},
				"professor": map[string]any{
					"source": map[string]any{"source": "github", "repo": "rezzminator/professor"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(first, "plugins", "known_marketplaces.json"), string(known))
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
			}
			if test.preview {
				for _, name := range []string{"engine-sync", "auto-effort-control"} {
					want := "  change  disable " + name + "@" + name + "-dev in "
					if !strings.Contains(out.String(), want) {
						t.Fatalf("preview lacks %q:\n%s", want, out.String())
					}
				}
				return
			}
			if got, want := pluginArgvs(runner), test.wantCalls(marketplace); !reflect.DeepEqual(got, want) {
				t.Fatalf("calls\n got %q\nwant %q\n%s", got, want, out.String())
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(readFixture(t, store)), &settings); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(settings["enabledPlugins"], test.wantEnabled) {
				t.Fatalf("enabledPlugins = %v, want %v\n%s", settings["enabledPlugins"], test.wantEnabled, out.String())
			}
			// forward-compaction retires sub-agent-compact, so its options settle on the -dev copy alone.
			wantConfigs := map[string]any{
				"forward-compaction@forward-compaction-dev": map[string]any{
					"options": map[string]any{"ceilingNative": true},
				},
				"engine-sync@professor":       map[string]any{"options": map[string]any{"y": float64(2)}},
				"engine-sync@engine-sync-dev": map[string]any{"options": map[string]any{"y": float64(2)}},
				"buddy@buddy-dev":             map[string]any{"options": map[string]any{"x": float64(1)}},
			}
			if !reflect.DeepEqual(settings["pluginConfigs"], wantConfigs) {
				t.Fatalf("pluginConfigs = %v, want %v", settings["pluginConfigs"], wantConfigs)
			}
			gaps, err := ClaudePluginMarketplaceGaps(first, ClaudePluginTargets(ClaudePluginBuild{
				Version: alphaVersion, Home: home, SourceRepo: inst.options.SourceRepo, Plugins: bareList(names...),
			}))
			if err != nil || len(gaps) != 0 {
				t.Fatalf("marketplaces left foreign: %v %v", gaps, err)
			}
			for _, name := range names {
				if got := pluginTreeFiles(t, filepath.Join(marketplace(name), "plugins", name)); len(got) != 2 {
					t.Fatalf("%s copy = %v, want the checkout's two files", name, got)
				}
			}
			if !test.listDefault {
				for _, line := range []string{
					"  ok      claude plugin buddy@buddy-dev installed in " + first + ", kept disabled: claude.plugins does not list buddy\n",
					"  change  disable auto-effort-control@auto-effort-control-dev in " + filepath.Join(first, "settings.json") +
						": claude.plugins does not list auto-effort-control\n",
					"  change  disable engine-sync@professor in " + filepath.Join(first, "settings.json") +
						": engine-sync@engine-sync-dev replaces it; copy its pluginConfigs to engine-sync@engine-sync-dev\n",
				} {
					if !strings.Contains(out.String(), line) {
						t.Fatalf("missing %q:\n%s", line, out.String())
					}
				}
			}
		})
	}
}

// TestEnsureClaudePluginsFailsAListedPluginLeftOff: after an applied -alpha
// install, a plugin claude.plugins lists that is not installed from its -dev
// copy and enabled is a FAIL naming the plugin, the account dir and each cause,
// never a quiet skip. A dry run and a plugin left right fail nothing.
func TestEnsureClaudePluginsFailsAListedPluginLeftOff(t *testing.T) {
	const name = "forward-compaction"
	dev := devID(name)
	for _, test := range []struct {
		name      string
		checkout  bool
		simulate  bool
		apply     bool
		wantCause []string
	}{
		{"its checkout is missing", false, true, true, []string{
			"plugin " + name + " falls back to the GitHub copy " + name + "@" + name + " on this alpha build: no checkout at ",
		}},
		{"the install left it disabled", true, false, true, []string{
			"plugin " + dev + " not installed in {first}", "plugin " + dev + " not enabled in {first}/settings.json",
		}},
		{"a dry run", true, false, false, nil},
		{"installed and enabled", true, true, true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			runner := &pluginRunner{}
			var out bytes.Buffer
			inst := pluginEngine(home, binary, first, second, runner, &out, test.apply)
			inst.options.Version = alphaVersion
			inst.options.ClaudePlugins = bareList(name)
			var checkouts []string
			if test.checkout {
				checkouts = []string{name}
			}
			inst.options.SourceRepo = writeMarketplaceClone(t, []string{name}, checkouts)
			if test.simulate {
				simulateClaude(t, runner)
			}
			err := inst.ensureClaudePlugins(context.Background())
			if test.wantCause == nil {
				if err != nil || strings.Contains(out.String(), "FAIL") {
					t.Fatalf("err = %v, want none\n%s", err, out.String())
				}
				return
			}
			id := dev
			if !test.checkout {
				id = name + "@" + name
			}
			prefix := "  FAIL    claude plugin " + id + " in " + first +
				": not installed from its -dev copy and enabled after this install: "
			line := ""
			for _, candidate := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(candidate, prefix) {
					line = candidate
				}
			}
			if err == nil || line == "" || !strings.Contains(err.Error(), strings.TrimPrefix(line, "  FAIL    ")) {
				t.Fatalf("err = %v, want the FAIL line %q…\n%s", err, prefix, out.String())
			}
			for _, cause := range test.wantCause {
				cause = strings.NewReplacer("{first}", first, "{second}", second).Replace(cause)
				if !strings.Contains(line, cause) {
					t.Fatalf("FAIL line %q lacks %q", line, cause)
				}
			}
		})
	}
}

// TestEnsureClaudePluginsAlphaUnlistedPluginLeftEnabled: an unlisted
// marketplace plugin found enabled is disabled; one with no checkout is never
// installed from GitHub while its enabled copy is still disabled, and a
// foreign -dev marketplace the add does not re-point is a failure naming what
// the remove would delete, never reported ensured.
func TestEnsureClaudePluginsAlphaUnlistedPluginLeftEnabled(t *testing.T) {
	for _, test := range []struct {
		name       string
		foreign    bool
		noCheckout bool
		want       []string
		wantErr    string
	}{
		{"an enabled unlisted plugin is disabled", false, false, []string{
			"  ok      claude plugin buddy@buddy-dev installed in {first}\n",
			"  change  disable buddy@buddy-dev in {first}/settings.json: claude.plugins does not list buddy\n",
		}, ""},
		{"no checkout: never installed from GitHub, its copy still disabled", false, true, []string{
			"  skip    claude plugin buddy: not installed — claude.plugins does not list it and this alpha build has no checkout of it: no checkout at ",
			"disable buddy@buddy-dev in {first}/settings.json",
		}, ""},
		{
			"a re-point that does not take", true, false, nil,
			"claude plugin buddy@buddy-dev in {first}: plugin buddy@buddy-dev marketplace buddy-dev in " +
				"{first}/plugins/known_marketplaces.json points at /elsewhere/buddy-dev, want {home}/.local/share/buddy-dev" +
				" after the add — claude plugin marketplace remove buddy-dev deletes the saved options and data of its plugins",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			runner := &pluginRunner{}
			var out bytes.Buffer
			inst := pluginEngine(home, binary, first, second, runner, &out, true)
			inst.options.Version = alphaVersion
			names := []string{"forward-compaction", "buddy"}
			withCheckout := names
			if test.noCheckout {
				withCheckout = names[:1]
			}
			inst.options.SourceRepo = writeMarketplaceClone(t, names, withCheckout)
			inst.options.ClaudePlugins = bareList("forward-compaction")
			writeFixture(t, filepath.Join(first, "settings.json"),
				`{"enabledPlugins":{"forward-compaction@forward-compaction-dev":true,"buddy@buddy-dev":true}}`)
			writeInstalledPlugins(t, first, "forward-compaction@forward-compaction-dev", "buddy@buddy-dev")
			buddy := filepath.Join(home, ".local", "share", "buddy-dev")
			if test.foreign {
				buddy = "/elsewhere/buddy-dev"
			}
			known, err := json.Marshal(map[string]any{
				"forward-compaction-dev": map[string]any{"source": map[string]any{
					"source": "directory", "path": filepath.Join(home, ".local", "share", "forward-compaction-dev"),
				}},
				"buddy-dev": map[string]any{"source": map[string]any{"source": "directory", "path": buddy}},
			})
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(first, "plugins", "known_marketplaces.json"), string(known))
			err = inst.ensureClaudePlugins(context.Background())
			replace := strings.NewReplacer("{first}", first, "{second}", second, "{home}", home)
			if want := replace.Replace(test.wantErr); want == "" && err != nil ||
				want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
				t.Fatalf("err = %v, want %q\n%s", err, want, out.String())
			}
			for _, line := range test.want {
				if line = replace.Replace(line); !strings.Contains(out.String(), line) {
					t.Fatalf("missing %q:\n%s", line, out.String())
				}
			}
			for _, call := range runner.calls {
				if strings.Contains(call.argv, "rezzminator/buddy") || strings.Contains(call.argv, "install buddy@") {
					t.Fatalf("unlisted buddy installed: %q\n%s", call.argv, out.String())
				}
			}
			if test.foreign && strings.Contains(out.String(), "  ok      claude plugin buddy@buddy-dev") {
				t.Fatalf("buddy reported ensured while its marketplace points elsewhere:\n%s", out.String())
			}
			if strings.Contains(out.String(), "kept disabled") {
				t.Fatalf("buddy reported kept disabled while enabled:\n%s", out.String())
			}
		})
	}
}
