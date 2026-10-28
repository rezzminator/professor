package installer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// simulateClaudeLegacy extends simulateClaude with the commands a legacy move
// runs, as Claude Code 2.1 does them: `plugin install ID --scope S -y` records
// ID in the project it runs in; `plugin uninstall ID --scope S --keep-data`
// drops that scope's record, and at user scope the id's enabledPlugins and
// pluginConfigs keys; `plugin marketplace remove M` drops M and every plugin
// recorded from it with its keys, leaving a key nothing records.
func simulateClaudeLegacy(t *testing.T, runner *pluginRunner) {
	t.Helper()
	simulateClaude(t, runner)
	install := runner.onRun
	edit := func(path string, change func(document map[string]any)) {
		document := map[string]any{}
		if raw, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Errorf("simulated claude: parse %s: %v", path, err)
			}
		}
		change(document)
		raw, err := json.Marshal(document)
		if err != nil {
			t.Errorf("simulated claude: %v", err)
			return
		}
		writeFixture(t, path, string(raw))
	}
	dropKeys := func(configDir, id string) {
		edit(filepath.Join(configDir, "settings.json"), func(document map[string]any) {
			for _, key := range []string{"enabledPlugins", "pluginConfigs"} {
				if entries, ok := document[key].(map[string]any); ok {
					delete(entries, id)
				}
			}
		})
	}
	records := func(configDir string) string {
		return filepath.Join(configDir, "plugins", "installed_plugins.json")
	}
	runner.onRun = func(configDir, argv string) {
		if _, failed := runner.fail[configDir+" "+argv]; failed {
			return // a command that fails changes nothing
		}
		dir := runner.dirs[len(runner.dirs)-1]
		fields := strings.Fields(argv)
		switch {
		case len(fields) == 6 && fields[1] == "install" && fields[3] == "--scope":
			name, _, _ := strings.Cut(fields[2], "@")
			installPath := filepath.Join(configDir, "plugins", "cache", fields[2])
			copyDir := filepath.Join(
				filepath.Dir(filepath.Dir(configDir)),
				".local",
				"share",
				"professor-dev",
				"plugins",
				name,
			)
			if err := mirrorPluginTree(copyDir, installPath); err != nil {
				t.Errorf("simulated claude: %v", err)
			}
			edit(records(configDir), func(document map[string]any) {
				plugins, _ := document["plugins"].(map[string]any)
				entries, _ := plugins[fields[2]].([]any)
				plugins[fields[2]] = append(entries, map[string]any{
					"scope": fields[4], "projectPath": dir, "installPath": installPath, "version": checkoutVersion,
				})
			})
		case len(fields) == 6 && fields[1] == "uninstall":
			edit(records(configDir), func(document map[string]any) {
				plugins, _ := document["plugins"].(map[string]any)
				entries, _ := plugins[fields[2]].([]any)
				var kept []any
				for _, entry := range entries {
					record, _ := entry.(map[string]any)
					if record["scope"] != fields[4] || fields[4] != "user" && record["projectPath"] != dir {
						kept = append(kept, entry)
					}
				}
				plugins[fields[2]] = kept
				if len(kept) == 0 {
					delete(plugins, fields[2])
				}
			})
			if fields[4] == "user" {
				dropKeys(configDir, fields[2])
			}
		case len(fields) == 4 && fields[1] == "marketplace" && fields[2] == "remove":
			edit(filepath.Join(configDir, "plugins", "known_marketplaces.json"), func(document map[string]any) {
				delete(document, fields[3])
			})
			edit(records(configDir), func(document map[string]any) {
				plugins, _ := document["plugins"].(map[string]any)
				for id := range plugins {
					if _, marketplace, _ := strings.Cut(id, "@"); marketplace == fields[3] {
						delete(plugins, id)
						dropKeys(configDir, id)
					}
				}
			})
		default:
			install(configDir, argv)
		}
	}
}

// writeLegacyHost lays out an -alpha host from before {name}@professor: each
// plugin on its {name}@{name}-dev copy (cache-live-control at user and project
// scope), a pfm-owned -dev directory marketplace each, its GitHub marketplace
// registered with nothing installed from it, options under both legacy ids,
// and the retired effort-live-control@effort-live-control installed and
// enabled. It returns the project directory.
func writeLegacyHost(t *testing.T, home, first string) string {
	t.Helper()
	project := t.TempDir()
	writeFixture(t, filepath.Join(first, "settings.json"), `{"enabledPlugins":{`+
		`"cache-live-control@cache-live-control-dev":true,"cache-live-control@cache-live-control":false,`+
		`"auto-effort-control@auto-effort-control-dev":true,"effort-live-control@effort-live-control":true},`+
		`"pluginConfigs":{"cache-live-control@cache-live-control-dev":{"options":{"ttl":"1h"}},`+
		`"cache-live-control@cache-live-control":{"options":{"ttl":"5m"}},`+
		`"auto-effort-control@auto-effort-control":{"options":{"floor":"medium"}}}}`)
	cache := filepath.Join(first, "plugins", "cache")
	installs, err := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{
		"cache-live-control@cache-live-control-dev": []any{
			map[string]any{"scope": "user", "installPath": filepath.Join(cache, "clc")},
			map[string]any{"scope": "project", "projectPath": project, "installPath": filepath.Join(cache, "clc")},
		},
		"auto-effort-control@auto-effort-control-dev": []any{
			map[string]any{"scope": "user", "installPath": filepath.Join(cache, "aec")},
		},
		"effort-live-control@effort-live-control": []any{
			map[string]any{"scope": "user", "installPath": filepath.Join(cache, "elc")},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(first, "plugins", "installed_plugins.json"), string(installs))
	known := map[string]any{
		"professor": map[string]any{
			"source": map[string]any{"source": "github", "repo": "rezzminator/professor"},
		},
		"effort-live-control": map[string]any{
			"source": map[string]any{"source": "github", "repo": "rezzminator/effort-live-control"},
		},
		"auto-effort-control": map[string]any{
			"source": map[string]any{"source": "github", "repo": "rezzminator/auto-effort"},
		},
		"cache-live-control": map[string]any{
			"source": map[string]any{"source": "github", "repo": "rezzminator/cache-live-control"},
		},
	}
	for _, name := range []string{"cache-live-control", "auto-effort-control"} {
		dir := filepath.Join(home, ".local", "share", name+"-dev")
		writeFixture(t, filepath.Join(dir, ".claude-plugin", "marketplace.json"),
			`{"name":"`+name+`-dev","plugins":[{"name":"`+name+`","source":"./plugins/`+name+`"}]}`)
		writePluginFiles(t, filepath.Join(dir, "plugins", name), name)
		known[name+"-dev"] = map[string]any{"source": map[string]any{"source": "directory", "path": dir}}
	}
	raw, err := json.Marshal(known)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(first, "plugins", "known_marketplaces.json"), string(raw))
	return project
}

// TestEnsureClaudePluginsMovesLegacyIDs: pfm install moves each plugin of ours
// from {name}@{name}-dev and {name}@{name} to {name}@professor: the enabled
// legacy id's options carried first, every install record uninstalled with its
// data kept (a project one installed as {name}@professor in its project first),
// each legacy marketplace pfm owns removed with its -dev directory, and every
// legacy key left in settings.json dropped. The retired effort-live-control
// stays installed and is disabled. A second run changes nothing, and doctor's
// legacy gaps go from named to none. A preview runs and writes nothing.
func TestEnsureClaudePluginsMovesLegacyIDs(t *testing.T) {
	names := []string{"cache-live-control", "auto-effort-control"}
	for _, preview := range []bool{false, true} {
		t.Run(map[bool]string{false: "applied", true: "preview"}[preview], func(t *testing.T) {
			inst, runner, out, home, first := alphaPluginEngine(t, "")
			inst.apply = !preview
			inst.options.SourceRepo = writeMarketplaceClone(t, names, names)
			inst.options.ClaudePlugins = bareList(names...)
			simulateClaudeLegacy(t, runner)
			project := writeLegacyHost(t, home, first)
			targets := ClaudePluginTargets(ClaudePluginBuild{
				Version: alphaVersion, Home: home, SourceRepo: inst.options.SourceRepo, Plugins: bareList(names...),
			})
			gaps, err := ClaudePluginLegacyGaps(first, home, targets)
			if err != nil || len(gaps) != len(names) {
				t.Fatalf("legacy gaps before = %v, %v; want one per plugin", gaps, err)
			}
			settingsBefore := readFixture(t, filepath.Join(first, "settings.json"))
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
			}
			marketplace := filepath.Join(home, ".local", "share", "professor-dev")
			settings := filepath.Join(first, "settings.json")
			if preview {
				if len(runner.calls) != 0 || readFixture(t, settings) != settingsBefore {
					t.Fatalf("a preview ran %q or wrote settings:\n%s", pluginArgvs(runner), out.String())
				}
				for _, line := range []string{
					"uninstall cache-live-control@cache-live-control-dev --scope user --keep-data: legacy id of cache-live-control@professor",
					"plugin marketplace remove auto-effort-control-dev: legacy marketplace of auto-effort-control@professor",
					"remove " + filepath.Join(home, ".local", "share", "cache-live-control-dev") + ": the legacy -dev marketplace",
				} {
					if !strings.Contains(out.String(), line) {
						t.Fatalf("preview lacks %q:\n%s", line, out.String())
					}
				}
				if _, err := os.Stat(filepath.Join(home, ".local", "share", "cache-live-control-dev")); err != nil {
					t.Fatalf("a preview removed the -dev directory: %v", err)
				}
				return
			}
			want := []string{
				"plugin marketplace add " + marketplace,
				"plugin install cache-live-control@professor -y",
				"plugin uninstall cache-live-control@cache-live-control-dev --scope user --keep-data",
				"plugin install cache-live-control@professor --scope project -y",
				"plugin uninstall cache-live-control@cache-live-control-dev --scope project --keep-data",
				"plugin marketplace remove cache-live-control",
				"plugin marketplace remove cache-live-control-dev",
				"plugin marketplace add " + marketplace,
				"plugin install auto-effort-control@professor -y",
				"plugin uninstall auto-effort-control@auto-effort-control-dev --scope user --keep-data",
				"plugin marketplace remove auto-effort-control",
				"plugin marketplace remove auto-effort-control-dev",
			}
			if got := pluginArgvs(runner); !reflect.DeepEqual(got, want) {
				t.Fatalf("calls\n got %q\nwant %q\n%s", got, want, out.String())
			}
			for index, call := range runner.calls {
				wantDir := ""
				if strings.Contains(call.argv, "--scope project") {
					wantDir = project
				}
				if runner.dirs[index] != wantDir {
					t.Fatalf("%q ran in %q, want %q", call.argv, runner.dirs[index], wantDir)
				}
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(readFixture(t, settings)), &document); err != nil {
				t.Fatal(err)
			}
			wantEnabled := map[string]any{
				"cache-live-control@professor":            true,
				"auto-effort-control@professor":           true,
				"effort-live-control@effort-live-control": false,
			}
			wantConfigs := map[string]any{
				"cache-live-control@professor":  map[string]any{"options": map[string]any{"ttl": "1h"}},
				"auto-effort-control@professor": map[string]any{"options": map[string]any{"floor": "medium"}},
			}
			if !reflect.DeepEqual(document["enabledPlugins"], wantEnabled) ||
				!reflect.DeepEqual(document["pluginConfigs"], wantConfigs) {
				t.Fatalf("settings = %v, want enabledPlugins %v and pluginConfigs %v\n%s",
					document, wantEnabled, wantConfigs, out.String())
			}
			for _, line := range []string{
				"  change  disable cache-live-control@cache-live-control-dev in " + settings +
					": cache-live-control@professor replaces it; copy its pluginConfigs to cache-live-control@professor\n",
				"  change  drop cache-live-control@cache-live-control from enabledPlugins; drop the pluginConfigs of " +
					"cache-live-control@cache-live-control (differs) in " + settings + ": legacy ids of cache-live-control@professor\n",
				"  change  disable auto-effort-control@auto-effort-control-dev in " + settings +
					": auto-effort-control@professor replaces it; copy the pluginConfigs of " +
					"auto-effort-control@auto-effort-control to auto-effort-control@professor\n",
				"  change  disable effort-live-control@effort-live-control in " + settings +
					": retired, auto-effort-control@professor replaces it\n",
			} {
				if !strings.Contains(out.String(), line) {
					t.Fatalf("missing %q:\n%s", line, out.String())
				}
			}
			records, err := readClaudePluginInstalls(first)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 3 || len(records["cache-live-control@professor"]) != 2 ||
				len(records["auto-effort-control@professor"]) != 1 ||
				len(records["effort-live-control@effort-live-control"]) != 1 {
				t.Fatalf("install records = %v, want cache-live-control@professor at two scopes, "+
					"auto-effort-control@professor and the retired effort-live-control", records)
			}
			_, known, err := readKnownMarketplaces(first)
			if err != nil {
				t.Fatal(err)
			}
			if len(known) != 2 || known["professor"].Source.Path != marketplace ||
				known["effort-live-control"].Source.Repo == "" {
				t.Fatalf(
					"known marketplaces = %v, want professor at %s and the retired effort-live-control",
					known,
					marketplace,
				)
			}
			for _, name := range names {
				if _, err := os.Stat(filepath.Join(home, ".local", "share", name+"-dev")); !os.IsNotExist(err) {
					t.Fatalf("legacy -dev directory of %s left: %v", name, err)
				}
			}
			if gaps, err := ClaudePluginLegacyGaps(first, home, targets); err != nil || len(gaps) != 0 {
				t.Fatalf("legacy gaps after = %v, %v; want none", gaps, err)
			}
			runner.calls, runner.dirs = nil, nil
			out.Reset()
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatalf("second ensureClaudePlugins: %v\n%s", err, out.String())
			}
			if len(runner.calls) != 0 || strings.Contains(out.String(), "  change  ") {
				t.Fatalf("a second run ran %q or changed something:\n%s", pluginArgvs(runner), out.String())
			}
		})
	}
}

// TestEnsureClaudePluginsKeepsALegacyIDItCannotMove: a legacy record whose
// uninstall fails keeps its id, its options, its marketplace and its -dev
// directory, and the run fails naming it, never reported moved.
func TestEnsureClaudePluginsKeepsALegacyIDItCannotMove(t *testing.T) {
	names := []string{"cache-live-control", "auto-effort-control"}
	inst, runner, out, home, first := alphaPluginEngine(t, "")
	inst.options.SourceRepo = writeMarketplaceClone(t, names, names)
	inst.options.ClaudePlugins = bareList(names...)
	simulateClaudeLegacy(t, runner)
	writeLegacyHost(t, home, first)
	runner.fail = map[string]string{
		first + " plugin uninstall cache-live-control@cache-live-control-dev --scope user --keep-data": "boom",
	}
	err := inst.ensureClaudePlugins(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cache-live-control@cache-live-control-dev --scope user") {
		t.Fatalf("err = %v, want the failed uninstall named\n%s", err, out.String())
	}
	for _, call := range pluginArgvs(runner) {
		if call == "plugin marketplace remove cache-live-control-dev" {
			t.Fatalf("removed the marketplace of an id it could not move:\n%s", out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "cache-live-control-dev")); err != nil {
		t.Fatalf("the -dev directory of an id it could not move is gone: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, filepath.Join(first, "settings.json"))), &document); err != nil {
		t.Fatal(err)
	}
	configs, _ := document["pluginConfigs"].(map[string]any)
	if _, kept := configs["cache-live-control@cache-live-control-dev"]; !kept {
		t.Fatalf("pluginConfigs of the id it could not move dropped: %v", configs)
	}
}
