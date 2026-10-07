package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const alphaVersion = "0.80.0-alpha"

// writePluginCheckout lays out {root}/{name}/plugins/{name} the way a plugin
// repository does, plus the node_modules and .DS_Store entries a mirror skips.
func writePluginCheckout(t *testing.T, root, name string) string {
	t.Helper()
	plugin := filepath.Join(root, name, "plugins", name)
	writeFixture(t, filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"`+name+`","version":"9.9.9"}`)
	writeFixture(t, filepath.Join(plugin, "hooks", "hook.ts"), "export const hook = '"+name+"'\n")
	if err := os.Chmod(filepath.Join(plugin, "hooks", "hook.ts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(plugin, "node_modules", "dep", "index.js"), "module.exports = 1\n")
	writeFixture(t, filepath.Join(plugin, ".DS_Store"), "finder")
	writeFixture(t, filepath.Join(plugin, "hooks", ".DS_Store"), "finder")
	return plugin
}

// pluginTreeFiles maps every regular file under root to its mode and content.
func pluginTreeFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s in the copy", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[rel] = fmt.Sprintf("%v %s", info.Mode().Perm(), content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func alphaPluginEngine(
	t *testing.T,
	root string,
) (inst *engine, runner *pluginRunner, out *bytes.Buffer, home, first string) {
	t.Helper()
	home, binary, first, second := pluginFixture(t)
	runner = &pluginRunner{}
	out = &bytes.Buffer{}
	inst = pluginEngine(home, binary, first, second, runner, out, true)
	inst.options.Version = alphaVersion
	inst.options.ClaudePluginCheckoutRoot = root
	return inst, runner, out, home, first
}

func devID(name string) string { return name + "@" + name + "-dev" }

func TestClaudePluginTargets(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	for _, p := range claudePlugins {
		writePluginCheckout(t, root, p.Name)
	}
	partial := t.TempDir()
	writePluginCheckout(t, partial, claudePlugins[0].Name)
	writePluginCheckout(t, partial, claudePlugins[2].Name)
	github := func(p claudePlugin, fallback string) ClaudePluginTarget {
		return ClaudePluginTarget{
			Name:     p.Name,
			ID:       p.ID,
			GitHubID: p.ID,
			DevID:    devID(p.Name),
			Source:   p.Source,
			Fallback: fallback,
		}
	}
	local := func(p claudePlugin, root string) ClaudePluginTarget {
		marketplace := filepath.Join(home, ".local", "share", p.Name+"-dev")
		return ClaudePluginTarget{
			Name: p.Name, ID: devID(p.Name), GitHubID: p.ID, DevID: devID(p.Name), Source: marketplace,
			Local: true, Marketplace: marketplace, Checkout: filepath.Join(root, p.Name, "plugins", p.Name),
		}
	}
	missing := filepath.Join(
		partial,
		claudePlugins[1].Name,
		"plugins",
		claudePlugins[1].Name,
		".claude-plugin",
		"plugin.json",
	)
	for _, test := range []struct {
		name  string
		build ClaudePluginBuild
		want  []ClaudePluginTarget
	}{
		{"release", ClaudePluginBuild{Version: "0.80.0", Home: home, CheckoutRoot: root}, []ClaudePluginTarget{
			github(claudePlugins[0], ""), github(claudePlugins[1], ""), github(claudePlugins[2], ""),
		}},
		{"unstamped dev build", ClaudePluginBuild{Version: "dev", Home: home, CheckoutRoot: root}, []ClaudePluginTarget{
			github(claudePlugins[0], ""), github(claudePlugins[1], ""), github(claudePlugins[2], ""),
		}},
		{"alpha without the key", ClaudePluginBuild{Version: alphaVersion, Home: home}, []ClaudePluginTarget{
			github(claudePlugins[0], "claude.pluginCheckoutRoot unset"),
			github(claudePlugins[1], "claude.pluginCheckoutRoot unset"),
			github(claudePlugins[2], "claude.pluginCheckoutRoot unset"),
		}},
		{"alpha with a checkout missing", ClaudePluginBuild{Version: alphaVersion, Home: home, CheckoutRoot: partial}, []ClaudePluginTarget{
			local(claudePlugins[0], partial),
			github(claudePlugins[1], "no checkout at "+missing+" (claude.pluginCheckoutRoot)"),
			local(claudePlugins[2], partial),
		}},
		{"alpha with every checkout", ClaudePluginBuild{Version: alphaVersion, Home: home, CheckoutRoot: root}, []ClaudePluginTarget{
			local(claudePlugins[0], root), local(claudePlugins[1], root), local(claudePlugins[2], root),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ClaudePluginTargets(test.build)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("targets\n got %+v\nwant %+v", got, test.want)
			}
		})
	}
}

func TestEnsureClaudePluginsAlphaInstallsTheLocalCopy(t *testing.T) {
	root := t.TempDir()
	inst, runner, out, home, first := alphaPluginEngine(t, root)
	checkouts := map[string]string{}
	for _, p := range claudePlugins {
		checkouts[p.Name] = writePluginCheckout(t, root, p.Name)
	}
	marketplace := func(name string) string { return filepath.Join(home, ".local", "share", name+"-dev") }
	mirror := func(name string) string { return filepath.Join(marketplace(name), "plugins", name) }
	// plugin 0: a hand-made marketplace whose plugin dir is a symlink, its manifest carrying a description.
	zero := claudePlugins[0].Name
	handManifest := `{"name":"` + zero + `-dev","description":"Local install.","owner":{"name":"rezzminator","url":"https://github.com/rezzminator"},` +
		`"plugins":[{"name":"` + zero + `","source":"./plugins/` + zero + `"}]}`
	writeFixture(t, filepath.Join(marketplace(zero), ".claude-plugin", "marketplace.json"), handManifest)
	if err := os.MkdirAll(filepath.Dir(mirror(zero)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(checkouts[zero], mirror(zero)); err != nil {
		t.Fatal(err)
	}
	// plugin 1: a stale copy with a file the checkout no longer has.
	one := claudePlugins[1].Name
	writeFixture(t, filepath.Join(mirror(one), ".claude-plugin", "plugin.json"), `{"name":"old"}`)
	writeFixture(t, filepath.Join(mirror(one), "old.txt"), "gone")
	if err := inst.ensureClaudePlugins(context.Background()); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	var want []pluginCall
	for _, p := range claudePlugins {
		want = append(want,
			pluginCall{"plugin marketplace add " + marketplace(p.Name), first},
			pluginCall{"plugin install " + devID(p.Name) + " -y", first},
		)
	}
	if fmt.Sprint(runner.calls) != fmt.Sprint(want) {
		t.Fatalf("calls=%v want %v\n%s", runner.calls, want, out.String())
	}
	wantFiles := map[string]string{}
	for _, p := range claudePlugins {
		wantFiles[p.Name] = fmt.Sprint(map[string]string{
			filepath.Join(".claude-plugin", "plugin.json"): "-rw------- " + `{"name":"` + p.Name + `","version":"9.9.9"}`,
			filepath.Join("hooks", "hook.ts"):              "-rwxr-xr-x export const hook = '" + p.Name + "'\n",
		})
		info, err := os.Lstat(mirror(p.Name))
		if err != nil || !info.IsDir() {
			t.Fatalf("%s copy is not a real directory: info=%v err=%v", p.Name, info, err)
		}
		if got := fmt.Sprint(pluginTreeFiles(t, mirror(p.Name))); got != wantFiles[p.Name] {
			t.Fatalf("%s copy\n got %s\nwant %s", p.Name, got, wantFiles[p.Name])
		}
		raw, err := os.ReadFile(filepath.Join(marketplace(p.Name), ".claude-plugin", "marketplace.json"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest map[string]any
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		wantManifest := map[string]any{
			"name":    p.Name + "-dev",
			"owner":   map[string]any{"name": "rezzminator", "url": "https://github.com/rezzminator"},
			"plugins": []any{map[string]any{"name": p.Name, "source": "./plugins/" + p.Name}},
		}
		if p.Name == zero {
			wantManifest["description"] = "Local install."
		}
		if !reflect.DeepEqual(manifest, wantManifest) {
			t.Fatalf("%s manifest = %v, want %v", p.Name, manifest, wantManifest)
		}
	}
	for _, line := range []string{
		"  change  replace symlink " + mirror(zero) + " with a copy of " + checkouts[zero] + "\n",
		"  change  refresh " + mirror(one) + " from " + checkouts[one] + "\n",
		"  change  copy " + checkouts[claudePlugins[2].Name] + " to " + mirror(claudePlugins[2].Name) + "\n",
		"  change  write " + filepath.Join(marketplace(one), ".claude-plugin", "marketplace.json") + "\n",
	} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("missing %q:\n%s", line, out.String())
		}
	}
	if strings.Contains(out.String(), "write "+filepath.Join(marketplace(zero), ".claude-plugin", "marketplace.json")) {
		t.Fatalf("a valid manifest was rewritten:\n%s", out.String())
	}

	// A second run with every -dev id installed and enabled runs nothing and
	// finds every copy current.
	enabled := map[string]any{}
	ids := []string{}
	for _, p := range claudePlugins {
		enabled[devID(p.Name)] = true
		ids = append(ids, devID(p.Name))
	}
	raw, _ := json.Marshal(map[string]any{"enabledPlugins": enabled})
	writeFixture(t, filepath.Join(first, "settings.json"), string(raw))
	writeInstalledPlugins(t, first, ids...)
	// Finder drops .DS_Store into a copy it shows; the copy still matches.
	writeFixture(t, filepath.Join(mirror(zero), ".DS_Store"), "finder")
	writeFixture(t, filepath.Join(mirror(zero), "hooks", ".DS_Store"), "finder")
	runner.calls = nil
	out.Reset()
	if err := inst.ensureClaudePlugins(context.Background()); err != nil {
		t.Fatalf("second run: %v\n%s", err, out.String())
	}
	if len(runner.calls) != 0 || strings.Contains(out.String(), "change") {
		t.Fatalf("second run changed something: calls=%v\n%s", runner.calls, out.String())
	}
	for _, p := range claudePlugins {
		for _, line := range []string{
			"  ok      claude plugin " + p.Name + " copy " + mirror(p.Name) + " matches " + checkouts[p.Name] + "\n",
			"  ok      claude plugin " + devID(p.Name) + " installed and enabled in " + first + "\n",
		} {
			if !strings.Contains(out.String(), line) {
				t.Fatalf("missing %q:\n%s", line, out.String())
			}
		}
	}
}

func TestEnsureClaudePluginsDisablesTheOtherCopy(t *testing.T) {
	github := func(p claudePlugin) string { return p.ID }
	for _, test := range []struct {
		name      string
		version   string
		withRoot  bool
		kept      func(claudePlugin) string
		other     func(claudePlugin) string
		checkouts bool
	}{
		{"alpha keeps -dev", alphaVersion, true, func(p claudePlugin) string { return devID(p.Name) }, github, true},
		{"release keeps GitHub", "0.80.0", true, github, func(p claudePlugin) string { return devID(p.Name) }, true},
		{"alpha fallback keeps GitHub", alphaVersion, false, github, func(p claudePlugin) string { return devID(p.Name) }, false},
	} {
		for _, apply := range []bool{true, false} {
			t.Run(fmt.Sprint(test.name, " apply=", apply), func(t *testing.T) {
				root := t.TempDir()
				configured := root
				if !test.withRoot {
					configured = ""
				}
				inst, runner, out, home, first := alphaPluginEngine(t, configured)
				inst.options.Version = test.version
				inst.apply = apply
				enabled := map[string]any{}
				ids := []string{}
				for _, p := range claudePlugins {
					if test.checkouts {
						writePluginCheckout(t, root, p.Name)
					}
					enabled[p.ID], enabled[devID(p.Name)] = true, true
					ids = append(ids, test.kept(p))
				}
				zero, one := claudePlugins[0], claudePlugins[1]
				document := map[string]any{
					"enabledPlugins": enabled,
					"pluginConfigs": map[string]any{
						test.other(zero): map[string]any{"options": map[string]any{"logFile": "/tmp/x <&>"}},
						test.other(one):  map[string]any{"options": map[string]any{"a": 1}},
						test.kept(one):   map[string]any{"options": map[string]any{"b": 2}},
					},
				}
				raw, _ := json.Marshal(document)
				// settings.json is the store's file, linked from the account dir.
				store := filepath.Join(home, ".claude", "settings.json")
				before := strings.Replace(string(raw), "{", `{"counter":9007199254740993,`, 1)
				writeFixture(t, store, before)
				if err := os.Symlink(store, filepath.Join(first, "settings.json")); err != nil {
					t.Fatal(err)
				}
				writeInstalledPlugins(t, first, ids...)
				if err := inst.ensureClaudePlugins(context.Background()); err != nil {
					t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
				}
				if len(runner.calls) != 0 {
					t.Fatalf("calls=%v, want none: every kept id is installed\n%s", runner.calls, out.String())
				}
				path := filepath.Join(first, "settings.json")
				for _, p := range claudePlugins {
					line := "  change  disable " + test.other(p) + " in " + path + ": " + test.kept(p) + " replaces it"
					if p.Name == zero.Name {
						line += "; copy its pluginConfigs to " + test.kept(p)
					}
					if !strings.Contains(out.String(), line+"\n") {
						t.Fatalf("missing %q:\n%s", line, out.String())
					}
				}
				if info, err := os.Lstat(path); err != nil || info.Mode()&fs.ModeSymlink == 0 {
					t.Fatalf("the account's settings link was replaced: %v %v", info, err)
				}
				after := readFixture(t, store)
				if !apply {
					if after != before {
						t.Fatalf("dry run wrote settings: %s", after)
					}
					return
				}
				if !strings.Contains(after, "9007199254740993") || !strings.Contains(after, "/tmp/x <&>") {
					t.Fatalf("settings lost a value: %s", after)
				}
				var got map[string]any
				if err := json.Unmarshal([]byte(after), &got); err != nil {
					t.Fatal(err)
				}
				wantEnabled := map[string]any{}
				for _, p := range claudePlugins {
					wantEnabled[test.other(p)], wantEnabled[test.kept(p)] = false, true
				}
				wantConfigs := map[string]any{
					test.other(zero): map[string]any{"options": map[string]any{"logFile": "/tmp/x <&>"}},
					test.kept(zero):  map[string]any{"options": map[string]any{"logFile": "/tmp/x <&>"}},
					test.other(one):  map[string]any{"options": map[string]any{"a": float64(1)}},
					test.kept(one):   map[string]any{"options": map[string]any{"b": float64(2)}},
				}
				if !reflect.DeepEqual(got["enabledPlugins"], wantEnabled) ||
					!reflect.DeepEqual(got["pluginConfigs"], wantConfigs) {
					t.Fatalf("settings = %v", got)
				}
				out.Reset()
				if err := inst.ensureClaudePlugins(
					context.Background(),
				); err != nil ||
					strings.Contains(out.String(), "disable") {
					t.Fatalf("second run err=%v:\n%s", err, out.String())
				}
			})
		}
	}
}

func TestEnsureClaudePluginsAlphaFallsBackToGitHub(t *testing.T) {
	for _, test := range []struct {
		name      string
		version   string
		withRoot  bool
		checkouts []int
		local     []bool
		reason    func(root string, p claudePlugin) string
	}{
		{
			"alpha without the key", alphaVersion, false, nil,
			[]bool{false, false, false},
			func(string, claudePlugin) string { return "claude.pluginCheckoutRoot unset" },
		},
		{
			"alpha with a checkout missing", alphaVersion, true,
			[]int{0, 2},
			[]bool{true, false, true},
			func(root string, p claudePlugin) string {
				return "no checkout at " + filepath.Join(root, p.Name, "plugins", p.Name, ".claude-plugin", "plugin.json") +
					" (claude.pluginCheckoutRoot)"
			},
		},
		{"release", "0.80.0", true, []int{0, 1, 2}, []bool{false, false, false}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, index := range test.checkouts {
				writePluginCheckout(t, root, claudePlugins[index].Name)
			}
			configured := root
			if !test.withRoot {
				configured = ""
			}
			inst, runner, out, home, first := alphaPluginEngine(t, configured)
			inst.options.Version = test.version
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
			}
			var want []pluginCall
			for index, p := range claudePlugins {
				fallback := "  skip    claude plugin " + p.Name + ": alpha build falls back to the GitHub copy " + p.ID
				if test.local[index] {
					want = append(
						want,
						pluginCall{
							"plugin marketplace add " + filepath.Join(home, ".local", "share", p.Name+"-dev"),
							first,
						},
						pluginCall{"plugin install " + devID(p.Name) + " -y", first},
					)
					if strings.Contains(out.String(), fallback) {
						t.Fatalf("local plugin %s reported a fallback:\n%s", p.Name, out.String())
					}
					continue
				}
				want = append(want,
					pluginCall{"plugin marketplace add " + p.Source, first},
					pluginCall{"plugin install " + p.ID + " -y", first},
				)
				if test.reason == nil {
					continue
				}
				if line := fallback + " — " + test.reason(root, p) + "\n"; !strings.Contains(out.String(), line) {
					t.Fatalf("missing %q:\n%s", line, out.String())
				}
			}
			if fmt.Sprint(runner.calls) != fmt.Sprint(want) {
				t.Fatalf("calls=%v want %v\n%s", runner.calls, want, out.String())
			}
			if test.reason == nil {
				if strings.Contains(out.String(), "falls back") {
					t.Fatalf("release build reported a fallback:\n%s", out.String())
				}
				if _, err := os.Stat(filepath.Join(home, ".local", "share")); err == nil {
					t.Fatalf("release build created a -dev marketplace:\n%s", out.String())
				}
			}
		})
	}
}
