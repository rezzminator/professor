package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestUnwireClaudePlugins is uninstall's take-back over two accounts whose
// settings.json and plugins/ link into the store: each row seeds the store's
// records and the directory marketplace, runs the step once and asserts its
// rows, every record after it (parsed, "" for absent), which paths remain and
// that both account links still point at the store.
func TestUnwireClaudePlugins(t *testing.T) {
	const (
		ownerSettings = `{"enabledPlugins":{"mine@other":true},"ownerKeep":"kept","theme":"dark"}`
		ownerInstalls = `{"version":2,"plugins":{"mine@other":[{"scope":"user","installPath":"{cc1}/plugins/cache/other/mine/2.0","version":"2.0"}]}}`
		ownerKnown    = `{"other":{"source":{"source":"github","repo":"o/other"},"installLocation":"{cc1}/plugins/marketplaces/other"}}`
		devManifest   = `{"name":"professor","plugins":[{"name":"flights","source":"./plugins/flights"}]}`
	)
	fullSettings := `{"enabledPlugins":{"auto-effort@professor":true,"flights@professor":false,"mine@other":true},` +
		`"ownerKeep":"kept","theme":"dark"}`
	fullInstalls := `{"version":2,"plugins":{` +
		`"auto-effort@professor":[{"scope":"user","installPath":"{cc1}/plugins/cache/professor/auto-effort/1.0.0","version":"1.0.0"}],` +
		`"flights@professor":[{"scope":"user","installPath":"{cc1}/plugins/cache/flights@professor"}],` +
		`"outside@professor":[{"scope":"user","installPath":"{home}/mine/outside"}],` +
		`"mine@other":[{"scope":"user","installPath":"{cc1}/plugins/cache/other/mine/2.0","version":"2.0"}]}}`
	fullKnown := `{"professor":{"source":{"source":"github","repo":"rezzminator/professor"},` +
		`"installLocation":"{cc1}/plugins/marketplaces/professor"},` +
		`"other":{"source":{"source":"github","repo":"o/other"},"installLocation":"{cc1}/plugins/marketplaces/other"}}`
	copies := []string{
		"{store}/plugins/cache/flights@professor", "{store}/plugins/cache/professor/auto-effort/1.0.0",
		"{store}/plugins/marketplaces/professor", "{store}/plugins/cache/professor", "{home}/mine/outside",
		"{store}/plugins/cache/other/mine/2.0", "{store}/plugins/marketplaces/other", "{dev}",
	}
	ownerCopies := []string{"{store}/plugins/cache/other/mine/2.0", "{store}/plugins/marketplaces/other"}
	takeRows := []string{
		"  change  remove auto-effort@professor, flights@professor from enabledPlugins in {cc1}/settings.json",
		"  change  remove auto-effort@professor, flights@professor, outside@professor from plugins in " +
			"{cc1}/plugins/installed_plugins.json",
		"  change  remove professor from {cc1}/plugins/known_marketplaces.json",
		"  change  remove {cc1}/plugins/cache/flights@professor",
		"  change  remove {cc1}/plugins/cache/professor/auto-effort/1.0.0",
		"  skip    claude plugin copy {home}/mine/outside lies outside {store}/plugins/cache and " +
			"{store}/plugins/marketplaces: not Claude's copy, kept",
		"  change  remove {cc1}/plugins/marketplaces/professor",
		"  change  remove {cc1}/plugins/cache/professor",
		"  change  remove {dev} (pfm's directory marketplace)",
	}
	for _, tc := range []struct {
		name                       string
		dryRun                     bool
		settings, installs, known  string
		manifest                   string
		rows                       []string
		wantErr                    bool
		wantSettings, wantInstalls string
		wantKnown                  string
		// seed is every copy made before the step ({dev} with manifest), remain each still there after it.
		seed, remain []string
	}{
		{
			name:     "the professor plugins, marketplace and copies go, the owner's stay",
			settings: fullSettings, installs: fullInstalls, known: fullKnown, manifest: devManifest,
			rows:         takeRows,
			wantSettings: ownerSettings, wantInstalls: ownerInstalls, wantKnown: ownerKnown,
			seed: copies, remain: append([]string{"{home}/mine/outside"}, ownerCopies...),
		},
		{
			name:     "a dry run names every removal and changes nothing",
			dryRun:   true,
			settings: fullSettings, installs: fullInstalls, known: fullKnown, manifest: devManifest,
			rows:         takeRows,
			wantSettings: fullSettings, wantInstalls: fullInstalls, wantKnown: fullKnown,
			seed: copies, remain: copies,
		},
		{
			name:     "records holding only the owner's plugins are left as they are",
			settings: ownerSettings, installs: ownerInstalls, known: ownerKnown,
			wantSettings: ownerSettings, wantInstalls: ownerInstalls, wantKnown: ownerKnown,
			seed: ownerCopies, remain: ownerCopies,
		},
		{
			name:     "an emptied enabledPlugins goes, and a directory marketplace that is not pfm's stays",
			settings: `{"enabledPlugins":{"flights@professor":true},"theme":"dark"}`,
			manifest: `{"name":"mine","plugins":[]}`,
			rows: []string{
				"  change  remove flights@professor from enabledPlugins in {cc1}/settings.json",
				`  skip    claude plugins: {dev} is not the professor marketplace pfm writes ` +
					`({dev}/.claude-plugin/marketplace.json: name "mine", <nil>), kept`,
			},
			wantSettings: `{"theme":"dark"}`,
			seed:         []string{"{dev}"}, remain: []string{"{dev}"},
		},
		{
			name:     "an unreadable install record fails each dir and keeps every file",
			settings: fullSettings, installs: `{"plugins":`, known: fullKnown, manifest: devManifest,
			rows: []string{
				"  FAIL    claude plugins in {cc1}: parse {cc1}/plugins/installed_plugins.json: unexpected end of JSON input",
				"  FAIL    claude plugins in {cc2}: parse {cc2}/plugins/installed_plugins.json: unexpected end of JSON input",
			},
			wantErr:      true,
			wantSettings: fullSettings, wantInstalls: `{"plugins":`, wantKnown: fullKnown,
			seed: copies, remain: copies,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(root, "home")
			store := filepath.Join(home, ".claude")
			cc1, cc2 := filepath.Join(home, ".cc", "1"), filepath.Join(home, ".cc", "2")
			dev := professorDevMarketplace(home)
			fill := strings.NewReplacer("{store}", store, "{cc1}", cc1, "{cc2}", cc2, "{home}", home, "{dev}", dev)
			records := map[string]string{
				filepath.Join(store, "settings.json"):                      tc.settings,
				filepath.Join(store, "plugins", "installed_plugins.json"):  tc.installs,
				filepath.Join(store, "plugins", "known_marketplaces.json"): tc.known,
			}
			for path, content := range records {
				if content != "" {
					writeFixture(t, path, fill.Replace(content))
				}
			}
			for _, path := range tc.seed {
				if path != "{dev}" {
					writeFixture(t, filepath.Join(fill.Replace(path), "plugin.json"), "{}")
				}
			}
			if tc.manifest != "" {
				writeFixture(t, filepath.Join(dev, ".claude-plugin", "marketplace.json"), tc.manifest)
				writeFixture(t, filepath.Join(dev, "plugins", "flights", "plugin.json"), "{}")
			}
			for _, dir := range []string{cc1, cc2} {
				for _, entry := range []string{"settings.json", "plugins"} {
					writeLink(t, filepath.Join(store, entry), filepath.Join(dir, entry))
				}
			}
			var output bytes.Buffer
			installer := &engine{apply: !tc.dryRun, options: Options{
				Mode: ModeUninstall, Home: home, ConfigDir: store, PrimaryConfigDir: cc1,
				ClaudeAccounts: []pfmconfig.Account{{ID: 1, ConfigDir: cc1}, {ID: 2, ConfigDir: cc2}},
				Stdout:         &output,
			}}
			if tc.dryRun {
				installer.options.Mode = ModeDryRun
			}
			err = installer.unwireClaudePlugins()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %t", err, tc.wantErr)
			}
			wantRows := fill.Replace(strings.Join(tc.rows, "\n"))
			if got := strings.TrimRight(output.String(), "\n"); got != wantRows {
				t.Fatalf("rows:\n%s\nwant:\n%s", got, wantRows)
			}
			for path, want := range map[string]string{
				filepath.Join(store, "settings.json"):                      tc.wantSettings,
				filepath.Join(store, "plugins", "installed_plugins.json"):  tc.wantInstalls,
				filepath.Join(store, "plugins", "known_marketplaces.json"): tc.wantKnown,
			} {
				assertRecord(t, path, fill.Replace(want))
			}
			for _, path := range tc.seed {
				_, statErr := os.Lstat(fill.Replace(path))
				if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("stat %s: %v", fill.Replace(path), statErr)
				}
				if remains, want := statErr == nil, slices.Contains(tc.remain, path); remains != want {
					t.Fatalf("%s present = %t after the step, want %t", fill.Replace(path), remains, want)
				}
			}
			for _, dir := range []string{cc1, cc2} {
				for _, entry := range []string{"settings.json", "plugins"} {
					if target, linkErr := os.Readlink(filepath.Join(dir, entry)); linkErr != nil ||
						target != filepath.Join(store, entry) {
						t.Fatalf("%s/%s -> %q (err %v), want a link into %s", dir, entry, target, linkErr, store)
					}
				}
			}
		})
	}
}

// writeLink links path to target, its parent made first.
func writeLink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(target, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

// assertRecord compares the JSON record at path with want, parsed, or its
// absence with an empty want; a record that is not JSON compares as bytes.
func assertRecord(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if want == "" {
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s = %q (err %v), want absent", path, raw, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var got, expected any
	if json.Unmarshal([]byte(want), &expected) != nil {
		if string(raw) != want {
			t.Fatalf("%s = %q, want %q", path, raw, want)
		}
		return
	}
	if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("%s = %s (err %v), want %s", path, raw, err, want)
	}
}
