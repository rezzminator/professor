package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// cacheFixtureEntry is one scope's entry of an install record fixture: its
// version, and the files its installPath holds, keyed by path; recorded, when
// set, overrides the fields the record holds; gone leaves its installPath
// absent.
type cacheFixtureEntry struct {
	scope, projectPath, version string
	files                       map[string]string
	recorded                    map[string]any
	gone                        bool
}

// writeCacheRecord writes dir's plugins/installed_plugins.json holding id
// alone, an entry per scope the way Claude records one: its snapshot under
// plugins/cache/{marketplace}/{name}/{version}.
func writeCacheRecord(t *testing.T, dir, id string, entries ...cacheFixtureEntry) {
	t.Helper()
	name, marketplace, _ := strings.Cut(id, "@")
	list := make([]any, 0, len(entries))
	for _, entry := range entries {
		installPath := filepath.Join(dir, "plugins", "cache", marketplace, name, entry.version)
		if !entry.gone {
			if err := os.MkdirAll(installPath, 0o700); err != nil {
				t.Fatal(err)
			}
			for rel, content := range entry.files {
				writeFixture(t, filepath.Join(installPath, rel), content)
			}
		}
		record := map[string]any{"scope": entry.scope, "installPath": installPath, "version": entry.version}
		if entry.projectPath != "" {
			record["projectPath"] = entry.projectPath
		}
		maps.Copy(record, entry.recorded)
		list = append(list, record)
	}
	raw, err := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{id: list}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "plugins", "installed_plugins.json"), string(raw))
}

// cacheFiles is the snapshot of writePluginFiles' tree at version, plus the
// entries the cache compare leaves out.
func cacheFiles(name, version, hook string) map[string]string {
	return map[string]string{
		filepath.Join(".claude-plugin", "plugin.json"): `{"name":"` + name + `","version":"` + version + `"}`,
		filepath.Join("hooks", "hook.ts"):              hook,
		filepath.Join("node_modules", "dep", "x.js"):   "cached dependency\n",
		".orphaned_at": "1760000000000",
		".DS_Store":    "finder",
	}
}

// simulateClaudeUpdate plays `claude plugin update {id} --scope {scope} -y`:
// the entry of that scope — of the working directory for a project scope —
// moves to the version of the plugin.json in its marketplace copy, with a
// fresh snapshot of that copy. `claude plugin install {id} -y` does the same
// for the user scope, adding its entry when there is none.
func simulateClaudeUpdate(t *testing.T, runner *pluginRunner, home string) {
	t.Helper()
	runner.onRun = func(configDir, argv string) {
		fields := strings.Fields(argv)
		install := len(fields) == 4 && fields[1] == "install"
		if !install && (len(fields) != 6 || fields[1] != "update" || fields[3] != "--scope") {
			return
		}
		id, scope, cwd := fields[2], "user", runner.dirs[len(runner.dirs)-1]
		if !install {
			scope = fields[4]
		}
		name, marketplace, _ := strings.Cut(id, "@")
		copyDir := filepath.Join(home, ".local", "share", marketplace, "plugins", name)
		path := filepath.Join(configDir, "plugins", "installed_plugins.json")
		var record struct {
			Version int                         `json:"version"`
			Plugins map[string][]map[string]any `json:"plugins"`
		}
		raw, err := os.ReadFile(path)
		if err == nil {
			err = json.Unmarshal(raw, &record)
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err == nil {
			raw, err = os.ReadFile(filepath.Join(copyDir, ".claude-plugin", "plugin.json"))
		}
		if err == nil {
			err = json.Unmarshal(raw, &manifest)
		}
		if err != nil {
			t.Errorf("simulated claude update: %v", err)
			return
		}
		installPath := filepath.Join(configDir, "plugins", "cache", marketplace, name, manifest.Version)
		if err := mirrorPluginTree(copyDir, installPath); err != nil {
			t.Errorf("simulated claude update: %v", err)
		}
		matched := false
		for _, entry := range record.Plugins[id] {
			if entry["scope"] != scope || scope != "user" && entry["projectPath"] != cwd {
				continue
			}
			matched = true
			entry["installPath"], entry["version"] = installPath, manifest.Version
		}
		if install && !matched {
			record.Plugins[id] = append(record.Plugins[id],
				map[string]any{"scope": "user", "installPath": installPath, "version": manifest.Version})
		}
		raw, err = json.Marshal(record)
		if err != nil {
			t.Errorf("simulated claude update: %v", err)
			return
		}
		writeFixture(t, path, string(raw))
	}
}

// cacheRecordVersions lists the version of every entry configDir records for id.
func cacheRecordVersions(t *testing.T, configDir, id string) []string {
	t.Helper()
	var record struct {
		Plugins map[string][]struct {
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(
		[]byte(readFixture(t, filepath.Join(configDir, "plugins", "installed_plugins.json"))), &record,
	); err != nil {
		t.Fatal(err)
	}
	versions := []string{}
	for _, entry := range record.Plugins[id] {
		versions = append(versions, entry.Version)
	}
	return versions
}

// TestEnsureClaudePluginsBringsTheCacheToTheCheckout: Claude runs an installed
// -dev plugin from the snapshot it records per scope in installed_plugins.json,
// so an -alpha install runs `claude plugin update {id} --scope {scope} -y` for
// every entry whose version is not the checkout's — a project scope's from its
// project dir — and reads the record back: an entry still behind is a failure
// naming both versions. An entry at the checkout's version whose snapshot
// differs from the checkout is a warning to bump the version, since Claude
// re-snapshots by version only; a preview names each update and runs none.
func TestEnsureClaudePluginsBringsTheCacheToTheCheckout(t *testing.T) {
	const name = "sub-agent-compact"
	id := devID(name)
	hook := "export const hook = '" + name + "'\n"
	for _, test := range []struct {
		name      string
		entries   func(project string) []cacheFixtureEntry
		apply     bool
		simulate  bool
		wantCalls func(project string) []string
		wantDirs  func(project string) []string
		wantLines []string
		wantErr   []string
		versions  []string
		// failScope is the scope whose update exits 1; unversioned checkouts
		// name no version; stray adds a file to each snapshot an update takes.
		failScope   string
		unversioned bool
		stray       bool
	}{
		{
			name: "stale user and project entries",
			entries: func(project string) []cacheFixtureEntry {
				return []cacheFixtureEntry{
					{scope: "user", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")},
					{scope: "project", projectPath: project, version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")},
				}
			},
			apply: true, simulate: true,
			wantCalls: func(string) []string {
				return []string{"plugin update " + id + " --scope user -y", "plugin update " + id + " --scope project -y"}
			},
			wantDirs: func(project string) []string { return []string{"", project} },
			wantLines: []string{
				"  change  run CLAUDE_CONFIG_DIR={first} {binary} plugin update " + id + " --scope user -y: ",
				"  change  run cd {project} && CLAUDE_CONFIG_DIR={first} {binary} plugin update " + id +
					" --scope project -y: ",
			},
			versions: []string{checkoutVersion, checkoutVersion},
		},
		{
			name: "an entry naming no version string",
			entries: func(string) []cacheFixtureEntry {
				return []cacheFixtureEntry{
					{scope: "user", version: "1.0.0", recorded: map[string]any{"version": 7}, files: cacheFiles(name, "1.0.0", "old\n")},
				}
			},
			apply: true, simulate: true,
			wantCalls: func(string) []string { return []string{"plugin update " + id + " --scope user -y"} },
			wantDirs:  func(string) []string { return []string{""} },
			wantLines: []string{
				"  change  run CLAUDE_CONFIG_DIR={first} {binary} plugin update " + id + " --scope user -y: plugin " + id +
					" cache in {first}/plugins/installed_plugins.json records no version string (scope user), checkout" +
					" {checkout} is " + checkoutVersion + "\n",
			},
			versions: []string{checkoutVersion},
		},
		{
			name: "an update that leaves the entry behind",
			entries: func(string) []cacheFixtureEntry {
				return []cacheFixtureEntry{{scope: "user", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")}}
			},
			apply:     true,
			wantCalls: func(string) []string { return []string{"plugin update " + id + " --scope user -y"} },
			wantDirs:  func(string) []string { return []string{""} },
			wantErr: []string{
				"claude plugin " + id + " in {first}: plugin " + id + " cache in {first}/plugins/installed_plugins.json" +
					" records version 1.0.0 (scope user), checkout {checkout} is " + checkoutVersion +
					" after claude plugin update",
			},
			versions: []string{"1.0.0"},
		},
		{
			name: "an equal version whose snapshot differs",
			entries: func(string) []cacheFixtureEntry {
				return []cacheFixtureEntry{{scope: "user", version: checkoutVersion, files: cacheFiles(name, checkoutVersion, "old\n")}}
			},
			apply:     true,
			wantCalls: func(string) []string { return []string{} },
			wantDirs:  func(string) []string { return []string{} },
			wantLines: []string{
				"  warn    claude plugin " + id + " in {first}: plugin " + id + " cache {first}/plugins/cache/" +
					name + "-dev/" + name + "/" + checkoutVersion + " differs from {checkout} at version " + checkoutVersion +
					" — bump the version in {checkout}/.claude-plugin/plugin.json: Claude caches by version;" +
					" then run pfm install --yes\n",
			},
			versions: []string{checkoutVersion},
		},
		{
			name: "a current snapshot",
			entries: func(string) []cacheFixtureEntry {
				return []cacheFixtureEntry{{scope: "user", version: checkoutVersion, files: cacheFiles(name, checkoutVersion, hook)}}
			},
			apply:     true,
			wantCalls: func(string) []string { return []string{} },
			wantDirs:  func(string) []string { return []string{} },
			versions:  []string{checkoutVersion},
		},
		{
			name: "a project entry whose project is gone, listed before the user entry",
			entries: func(project string) []cacheFixtureEntry {
				return []cacheFixtureEntry{
					{scope: "project", projectPath: project + "-gone\x1b[2J", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")},
					{scope: "user", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")},
				}
			},
			apply: true, simulate: true,
			wantCalls: func(string) []string { return []string{"plugin update " + id + " --scope user -y"} },
			wantDirs:  func(string) []string { return []string{""} },
			wantErr: []string{
				"records version 1.0.0 (scope project {project}-gone\\x1b[2J), checkout {checkout} is " + checkoutVersion +
					" — its project {project}-gone\\x1b[2J is not an absolute directory: recreate it and run pfm install" +
					" --yes, or remove the entry with claude plugin uninstall " + id + " --scope project from that project",
			},
			versions: []string{"1.0.0", checkoutVersion},
		},
		{
			name: "an unknown scope listed first",
			entries: func(string) []cacheFixtureEntry {
				return []cacheFixtureEntry{
					{scope: "managed", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")},
					{scope: "user", version: "1.0.0", recorded: map[string]any{"projectPath": 7}, files: cacheFiles(name, "1.0.0", "old\n")},
				}
			},
			apply: true, simulate: true,
			wantCalls: func(string) []string { return []string{"plugin update " + id + " --scope user -y"} },
			wantDirs:  func(string) []string { return []string{""} },
			wantErr: []string{
				"records version 1.0.0 (scope managed), checkout {checkout} is " + checkoutVersion +
					" — claude plugin update takes no scope managed: update it the way it was installed",
			},
			versions: []string{"1.0.0", checkoutVersion},
		},
		{
			name: "a failed update before another scope",
			entries: func(project string) []cacheFixtureEntry {
				return []cacheFixtureEntry{
					{scope: "project", projectPath: project, version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")},
					{scope: "user", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")},
				}
			},
			apply: true, simulate: true, failScope: "project",
			wantCalls: func(string) []string {
				return []string{"plugin update " + id + " --scope project -y", "plugin update " + id + " --scope user -y"}
			},
			wantDirs: func(project string) []string { return []string{project, ""} },
			wantErr:  []string{"plugin update " + id + " --scope project -y: exit 1"},
			versions: []string{checkoutVersion, checkoutVersion},
		},
		{
			name: "a fresh install leaving a project entry behind",
			entries: func(project string) []cacheFixtureEntry {
				return []cacheFixtureEntry{
					{scope: "project", projectPath: project, version: "1.0.0", gone: true},
				}
			},
			apply: true, simulate: true,
			wantCalls: func(project string) []string {
				return []string{
					"plugin marketplace add " + filepath.Join(filepath.Dir(project), ".local", "share", name+"-dev"),
					"plugin install " + id + " -y", "plugin update " + id + " --scope project -y",
				}
			},
			wantDirs: func(project string) []string { return []string{"", "", project} },
			versions: []string{checkoutVersion, checkoutVersion},
		},
		{
			name: "an update whose new snapshot differs",
			entries: func(string) []cacheFixtureEntry {
				return []cacheFixtureEntry{{scope: "user", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")}}
			},
			apply: true, simulate: true, stray: true,
			wantCalls: func(string) []string { return []string{"plugin update " + id + " --scope user -y"} },
			wantDirs:  func(string) []string { return []string{""} },
			wantLines: []string{
				"  warn    claude plugin " + id + " in {first}: plugin " + id + " cache {first}/plugins/cache/" +
					name + "-dev/" + name + "/" + checkoutVersion + " differs from {checkout} at version " + checkoutVersion,
			},
			versions: []string{checkoutVersion},
		},
		{
			name: "a checkout naming no version",
			entries: func(string) []cacheFixtureEntry {
				return []cacheFixtureEntry{{scope: "user", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")}}
			},
			apply: true, simulate: true, unversioned: true,
			wantCalls: func(string) []string { return []string{} },
			wantDirs:  func(string) []string { return []string{} },
			wantLines: []string{
				"  skip    claude plugin " + id + " in {first}: plugin " + id + " cache in {first}/plugins/installed_plugins.json" +
					" cannot be checked: {checkout}/.claude-plugin/plugin.json names no version, and Claude caches by version" +
					" — add a version to {checkout}/.claude-plugin/plugin.json, then run pfm install --yes\n",
			},
			versions: []string{"1.0.0"},
		},
		{
			name: "a preview",
			entries: func(string) []cacheFixtureEntry {
				return []cacheFixtureEntry{{scope: "user", version: "1.0.0", files: cacheFiles(name, "1.0.0", "old\n")}}
			},
			wantCalls: func(string) []string { return []string{} },
			wantDirs:  func(string) []string { return []string{} },
			wantLines: []string{
				"  change  run CLAUDE_CONFIG_DIR={first} {binary} plugin update " + id + " --scope user -y: plugin " + id +
					" cache in {first}/plugins/installed_plugins.json records version 1.0.0 (scope user), checkout {checkout} is " +
					checkoutVersion + "\n",
			},
			versions: []string{"1.0.0"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			inst, runner, out, home, first := alphaPluginEngine(t, root)
			inst.apply = test.apply
			inst.options.ClaudePlugins = bareList(name)
			checkout := writePluginCheckout(t, root, name)
			if test.unversioned {
				writeFixture(t, filepath.Join(checkout, ".claude-plugin", "plugin.json"), `{"name":"`+name+`"}`)
			}
			project := filepath.Join(home, "project")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(first, "settings.json"), `{"enabledPlugins":{"`+id+`":true}}`)
			known, err := json.Marshal(map[string]any{name + "-dev": map[string]any{"source": map[string]any{
				"source": "directory", "path": filepath.Join(home, ".local", "share", name+"-dev"),
			}}})
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(first, "plugins", "known_marketplaces.json"), string(known))
			writeCacheRecord(t, first, id, test.entries(project)...)
			if test.simulate {
				simulateClaudeUpdate(t, runner, home)
			}
			if test.stray {
				update := runner.onRun
				runner.onRun = func(configDir, argv string) {
					update(configDir, argv)
					writeFixture(
						t,
						filepath.Join(configDir, "plugins", "cache", name+"-dev", name, checkoutVersion, "stray.js"),
						"x\n",
					)
				}
			}
			if test.failScope != "" {
				runner.fail = map[string]string{
					first + " plugin update " + id + " --scope " + test.failScope + " -y": "boom",
				}
			}
			err = inst.ensureClaudePlugins(context.Background())
			replace := strings.NewReplacer(
				"{first}", first, "{binary}", inst.options.ClaudeBinary, "{checkout}", checkout, "{project}", project,
			)
			for _, want := range test.wantErr {
				if want = replace.Replace(want); err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want %q\n%s", err, want, out.String())
				}
			}
			if test.wantErr == nil && err != nil {
				t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
			}
			if got, want := pluginArgvs(runner), test.wantCalls(project); !reflect.DeepEqual(got, want) {
				t.Fatalf("commands\n got %q\nwant %q\n%s", got, want, out.String())
			}
			if got, want := runner.dirs, test.wantDirs(project); !slices.Equal(got, want) {
				t.Fatalf("working dirs %q, want %q", got, want)
			}
			for index, env := range runner.envs {
				if !slices.Contains(env, claudeConfigDirEnv+"="+first) {
					t.Fatalf("command %d ran without %s=%s: %q", index, claudeConfigDirEnv, first, env)
				}
			}
			for _, line := range test.wantLines {
				if line = replace.Replace(line); !strings.Contains(out.String(), line) {
					t.Fatalf("missing %q:\n%s", line, out.String())
				}
			}
			if test.wantLines == nil && strings.Contains(out.String(), "warn") {
				t.Fatalf("unexpected warning:\n%s", out.String())
			}
			if got := cacheRecordVersions(t, first, id); !reflect.DeepEqual(got, test.versions) {
				t.Fatalf("recorded versions %q, want %q\n%s", got, test.versions, out.String())
			}
		})
	}
}

// TestEnsureClaudePluginsUpdatesCallmetersCacheAfterItsBinary: the callmeter
// cache moves to a version only once the binary its hooks run is built.
func TestEnsureClaudePluginsUpdatesCallmetersCacheAfterItsBinary(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	t.Setenv("CALLMETER_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	root := t.TempDir()
	writeCallmeterCheckout(t, root, "0.4.0")
	runner := &goRunner{output: "new"}
	updatedBeforeBuild := false
	runner.onBuild = func() {
		updatedBeforeBuild = slices.ContainsFunc(runner.calls, func(call pluginCall) bool {
			return strings.HasPrefix(call.argv, "plugin update ")
		})
	}
	var out bytes.Buffer
	inst := pluginEngine(home, binary, first, second, runner, &out, true)
	inst.options.Version = alphaVersion
	inst.options.ClaudePluginCheckoutRoot = root
	inst.options.ClaudePlugins = bareList(callmeterPlugin)
	id := devID(callmeterPlugin)
	writeFixture(t, filepath.Join(first, "settings.json"), `{"enabledPlugins":{"`+id+`":true}}`)
	known, err := json.Marshal(map[string]any{callmeterPlugin + "-dev": map[string]any{"source": map[string]any{
		"source": "directory", "path": filepath.Join(home, ".local", "share", callmeterPlugin+"-dev"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(first, "plugins", "known_marketplaces.json"), string(known))
	writeCacheRecord(t, first, id, cacheFixtureEntry{scope: "user", version: "0.3.0", files: map[string]string{
		filepath.Join(".claude-plugin", "plugin.json"): `{"name":"callmeter","version":"0.3.0"}`,
	}})
	simulateClaudeUpdate(t, &runner.pluginRunner, home)
	if err := inst.ensureClaudePlugins(context.Background()); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	want := []string{"plugin update " + id + " --scope user -y"}
	got := pluginArgvs(&runner.pluginRunner)
	if len(runner.builds) != 1 || updatedBeforeBuild || !reflect.DeepEqual(got, want) {
		t.Fatalf("builds=%d updated before the build=%v commands %q, want %q\n%s",
			len(runner.builds), updatedBeforeBuild, got, want, out.String())
	}
	if got := cacheRecordVersions(t, first, id); !reflect.DeepEqual(got, []string{"0.4.0"}) {
		t.Fatalf("recorded versions %q, want 0.4.0", got)
	}
}
