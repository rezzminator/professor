package installer

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// TestRetireClaudePlugins is install's removal of a plugin retired with no
// successor over two accounts whose settings.json and plugins/ link into the
// store: each row seeds the store's records, Claude's copies and the plugin log
// folder, asserts the gaps doctor reads before the step, runs the step, asserts
// its rows, every record after it, which paths remain and the gaps after it,
// then runs it again and asserts it changes nothing more.
func TestRetireClaudePlugins(t *testing.T) {
	const (
		otherInstalls = `"tinsel@other":[{"scope":"user","installPath":"{cc1}/plugins/cache/other/tinsel/2.0"}]`
		ownInstalls   = `"tinsel@professor":[{"scope":"user","installPath":"{cc1}/plugins/cache/professor/tinsel/1.0"}]`
	)
	ownCopies := []string{
		"{store}/plugins/cache/professor/tinsel/1.0", "{store}/plugins/cache/professor/tinsel",
		"{store}/plugins/data/tinsel-professor",
	}
	otherCopies := []string{"{store}/plugins/cache/other/tinsel/2.0", "{store}/plugins/data/tinsel-other"}
	ownTraces := "enabledPlugins in {cc1}/settings.json, plugins in {cc1}/plugins/installed_plugins.json, " +
		"{cc1}/plugins/cache/professor/tinsel/1.0, {cc1}/plugins/cache/professor/tinsel, {cc1}/plugins/data/tinsel-professor"
	ownRows := []string{
		"  change  remove tinsel@professor from enabledPlugins in {cc1}/settings.json",
		"  change  remove tinsel@professor from plugins in {cc1}/plugins/installed_plugins.json",
		"  change  remove {cc1}/plugins/cache/professor/tinsel/1.0",
		"  change  remove {cc1}/plugins/cache/professor/tinsel",
		"  change  remove {cc1}/plugins/data/tinsel-professor",
	}
	gap := func(id, traces string) ClaudePluginGap {
		return ClaudePluginGap{
			Problem: "plugin " + id + " is retired with no successor but still on this host: " + traces,
			Fix:     "run pfm install --yes removes every trace of it",
		}
	}
	plannedRow := "  skip    claude plugins: retired plugin tinsel@professor kept — claude.plugins or the source " +
		"clone's marketplace still plans it; drop it there or from claudePlugins[\"(none)\"] in the retired lists"
	plannedGap := ClaudePluginGap{
		Problem: "plugin tinsel@professor is retired with no successor but claude.plugins or the source clone's " +
			"marketplace still plans it",
		Fix: `drop it there or from claudePlugins["(none)"] in the retired lists`,
	}
	keptRow := "  skip    claude plugins: {ownlogs}/tinsel kept — it holds files other than plugin logs, under a " +
		"PLUGIN_LOG_DIR other than pfm's own default; remove the retired plugin's logs there by hand"
	localSkipRow := "  skip    claude plugins: parse {cc2}/settings.local.json: unexpected EOF — pfm only retires " +
		"plugins there, so the file stays as it is; a retired plugin it enables stays enabled until the file reads " +
		"and pfm install --yes runs again"
	localGap := ClaudePluginGap{
		Problem: "parse {cc2}/settings.local.json: unexpected EOF, so pfm cannot tell whether it still enables a " +
			"plugin retired with no successor",
		Fix: "repair or remove that file, then run pfm install --yes",
	}
	sharedSettings := `{"enabledPlugins":{"flights@professor":true,"tinsel@other":true,"tinsel@professor":false},"theme":"dark"}`
	sharedInstalls := `{"version":2,"plugins":{` + ownInstalls + `,` + otherInstalls + `}}`
	for _, tc := range []struct {
		name                       string
		ids                        []string
		dryRun                     bool
		settings, installs         string
		rows                       []string
		wantErr                    bool
		wantSettings, wantInstalls string
		// gaps are doctor's before the step, gapsAfter after it.
		gaps, gapsAfter []ClaudePluginGap
		// seed is every copy made before the step, a folder holding seed.json or
		// a .jsonl file itself; remain each still there after it.
		seed, remain []string
		// plugins is claude.plugins; again the rows of the second run.
		plugins []pfmconfig.ClaudePlugin
		again   []string
		// ledger is the claude env ledger's pluginLogDir, "" pfm's default
		// {logs}; local is {cc2}/settings.local.json, "" none, wantLocal it
		// after the step; dangling makes {cc2}/settings.json a dangling link;
		// gapErr is doctor's exact error before the step.
		ledger, local, wantLocal, gapErr string
		dangling                         bool
	}{
		{
			name:   "a folder under a PLUGIN_LOG_DIR pfm.config chose that holds more than plugin logs stays",
			ids:    []string{"tinsel@professor"},
			ledger: "{ownlogs}", settings: `{"enabledPlugins":{"tinsel@professor":false},"theme":"dark"}`,
			installs:     `{"version":2,"plugins":{` + ownInstalls + `}}`,
			rows:         append(slices.Clone(ownRows), keptRow),
			again:        []string{keptRow},
			wantSettings: `{"theme":"dark"}`, wantInstalls: `{"version":2,"plugins":{}}`,
			gaps:   []ClaudePluginGap{gap("tinsel@professor", ownTraces)},
			seed:   append(slices.Clone(ownCopies), "{ownlogs}/tinsel"),
			remain: []string{"{ownlogs}/tinsel"},
		},
		{
			name: "a retired id enabled in an account's settings.local.json goes, its other keys stay",
			ids:  []string{"tinsel@professor"}, settings: `{"theme":"dark"}`, installs: `{"version":2,"plugins":{}}`,
			local:        `{"enabledPlugins":{"flights@professor":true,"tinsel@professor":true}}`,
			wantLocal:    `{"enabledPlugins":{"flights@professor":true}}`,
			rows:         []string{"  change  remove tinsel@professor from enabledPlugins in {cc2}/settings.local.json"},
			wantSettings: `{"theme":"dark"}`, wantInstalls: `{"version":2,"plugins":{}}`,
			gaps: []ClaudePluginGap{gap("tinsel@professor", "enabledPlugins in {cc2}/settings.local.json")},
		},
		{
			name: "a settings.local.json that does not parse is a skip row, the store's copies go and the log folder stays",
			ids:  []string{"tinsel@professor"}, settings: `{"enabledPlugins":{"tinsel@professor":false},"theme":"dark"}`,
			installs: `{"version":2,"plugins":{` + ownInstalls + `}}`,
			local:    `{"enabledPlugins":`, wantLocal: `{"enabledPlugins":`,
			rows: append(slices.Clone(ownRows), localSkipRow, "  skip    claude plugins: {logs}/tinsel kept — "+
				"{cc2}/settings.local.json blocks the check that no other plugin named tinsel still writes there: "+
				"parse {cc2}/settings.local.json: unexpected EOF"),
			again: []string{localSkipRow, "  skip    claude plugins: {logs}/tinsel kept — " +
				"{cc2}/settings.local.json blocks the check that no other plugin named tinsel still writes there: " +
				"parse {cc2}/settings.local.json: unexpected EOF"},
			wantSettings: `{"theme":"dark"}`, wantInstalls: `{"version":2,"plugins":{}}`,
			gaps:      []ClaudePluginGap{gap("tinsel@professor", ownTraces), localGap},
			gapsAfter: []ClaudePluginGap{localGap},
			seed:      append(slices.Clone(ownCopies), "{logs}/tinsel"), remain: []string{"{logs}/tinsel"},
		},
		{
			name: "a dangling settings.json link reads as absent, as the take reads it",
			ids:  []string{"tinsel@professor"}, dangling: true,
			settings:     `{"enabledPlugins":{"tinsel@professor":false},"theme":"dark"}`,
			installs:     `{"version":2,"plugins":{` + ownInstalls + `}}`,
			rows:         append(slices.Clone(ownRows), "  change  remove {logs}/tinsel (logs of a retired plugin)"),
			wantSettings: `{"theme":"dark"}`, wantInstalls: `{"version":2,"plugins":{}}`,
			gaps: []ClaudePluginGap{gap("tinsel@professor", ownTraces+", {logs}/tinsel")},
			seed: append(slices.Clone(ownCopies), "{logs}/tinsel"),
		},
		{
			name: "a disabled retired plugin goes everywhere, another marketplace's of its name and its log folder stay",
			ids:  []string{"tinsel@professor"}, settings: sharedSettings, installs: sharedInstalls,
			rows:         ownRows,
			wantSettings: `{"enabledPlugins":{"flights@professor":true,"tinsel@other":true},"theme":"dark"}`,
			wantInstalls: `{"version":2,"plugins":{` + otherInstalls + `}}`,
			gaps:         []ClaudePluginGap{gap("tinsel@professor", ownTraces)},
			seed:         append(slices.Clone(ownCopies), append(otherCopies, "{logs}/tinsel")...),
			remain:       append(slices.Clone(otherCopies), "{logs}/tinsel"),
		},
		{
			name: "a dry run names every removal and changes nothing",
			ids:  []string{"tinsel@professor"}, dryRun: true, settings: sharedSettings, installs: sharedInstalls,
			rows:         ownRows,
			wantSettings: sharedSettings, wantInstalls: sharedInstalls,
			gaps:      []ClaudePluginGap{gap("tinsel@professor", ownTraces)},
			gapsAfter: []ClaudePluginGap{gap("tinsel@professor", ownTraces)},
			seed:      append(slices.Clone(ownCopies), "{logs}/tinsel"),
			remain:    append(slices.Clone(ownCopies), "{logs}/tinsel"),
		},
		{
			name:     "an enabled retired plugin alone takes its emptied enabledPlugins, inline data and log folder",
			ids:      []string{"tinsel@professor", "tinsel@inline"},
			settings: `{"enabledPlugins":{"tinsel@professor":true},"theme":"dark"}`,
			installs: `{"version":2,"plugins":{` + ownInstalls + `}}`,
			rows: append(slices.Clone(ownRows),
				"  change  remove {cc1}/plugins/data/tinsel-inline",
				"  change  remove {logs}/tinsel (logs of a retired plugin)"),
			wantSettings: `{"theme":"dark"}`,
			wantInstalls: `{"version":2,"plugins":{}}`,
			gaps: []ClaudePluginGap{
				gap("tinsel@professor", ownTraces+", {logs}/tinsel"),
				gap("tinsel@inline", "{cc1}/plugins/data/tinsel-inline"),
			},
			seed: append(slices.Clone(ownCopies), "{store}/plugins/data/tinsel-inline", "{logs}/tinsel"),
		},
		{
			name:     "an operator's log folder in settings loses the retired folder too",
			ids:      []string{"tinsel@professor"},
			settings: `{"enabledPlugins":{"tinsel@professor":false},"env":{"PLUGIN_LOG_DIR":"{ownlogs}"}}`,
			installs: `{"version":2,"plugins":{` + ownInstalls + `}}`,
			rows: append(slices.Clone(ownRows),
				"  change  remove {logs}/tinsel (logs of a retired plugin)",
				"  change  remove {ownlogs}/tinsel (logs of a retired plugin)"),
			wantSettings: `{"env":{"PLUGIN_LOG_DIR":"{ownlogs}"}}`,
			wantInstalls: `{"version":2,"plugins":{}}`,
			gaps:         []ClaudePluginGap{gap("tinsel@professor", ownTraces+", {logs}/tinsel, {ownlogs}/tinsel")},
			seed: append(slices.Clone(ownCopies), "{logs}/tinsel", "{ownlogs}/tinsel/errors.jsonl",
				"{ownlogs}/tinsel/info.jsonl.1", "{ownlogs}/flights"),
			remain: []string{"{ownlogs}/flights"},
		},
		{
			name:     "an operator's folder holding more than plugin logs stays",
			ids:      []string{"tinsel@professor"},
			settings: `{"enabledPlugins":{"tinsel@professor":false},"env":{"PLUGIN_LOG_DIR":"{ownlogs}"}}`,
			installs: `{"version":2,"plugins":{` + ownInstalls + `}}`,
			rows: append(slices.Clone(ownRows),
				keptRow,
				"  change  remove {logs}/tinsel (logs of a retired plugin)"),
			wantSettings: `{"env":{"PLUGIN_LOG_DIR":"{ownlogs}"}}`,
			wantInstalls: `{"version":2,"plugins":{}}`,
			gaps:         []ClaudePluginGap{gap("tinsel@professor", ownTraces+", {logs}/tinsel")},
			again:        []string{keptRow},
			seed:         append(slices.Clone(ownCopies), "{logs}/tinsel", "{ownlogs}/tinsel/errors.jsonl", "{ownlogs}/tinsel"),
			remain:       []string{"{ownlogs}/tinsel/errors.jsonl", "{ownlogs}/tinsel"},
		},
		{
			name:     "a marketplace name Claude rewrites names its data dir as Claude does",
			ids:      []string{"tinsel@my.market"},
			settings: `{"theme":"dark"}`, installs: `{"version":2,"plugins":{}}`,
			rows: []string{
				"  change  remove {cc1}/plugins/cache/my.market/tinsel",
				"  change  remove {cc1}/plugins/data/tinsel-my-market",
			},
			wantSettings: `{"theme":"dark"}`, wantInstalls: `{"version":2,"plugins":{}}`,
			gaps: []ClaudePluginGap{gap("tinsel@my.market",
				"{cc1}/plugins/cache/my.market/tinsel, {cc1}/plugins/data/tinsel-my-market")},
			seed: []string{"{store}/plugins/cache/my.market/tinsel", "{store}/plugins/data/tinsel-my-market"},
		},
		{
			name: "a retired id claude.plugins still names is kept and named, with its log folder",
			ids:  []string{"tinsel@professor"}, plugins: []pfmconfig.ClaudePlugin{{ID: "tinsel"}},
			settings: sharedSettings, installs: sharedInstalls,
			rows:         []string{plannedRow},
			again:        []string{plannedRow},
			wantSettings: sharedSettings, wantInstalls: sharedInstalls,
			gaps:      []ClaudePluginGap{plannedGap},
			gapsAfter: []ClaudePluginGap{plannedGap},
			seed:      append(slices.Clone(ownCopies), "{logs}/tinsel"),
			remain:    append(slices.Clone(ownCopies), "{logs}/tinsel"),
		},
		{
			name:     "a data dir another installed id's name shares stays",
			ids:      []string{"tinsel-red@professor"},
			settings: `{"enabledPlugins":{"tinsel@red-professor":true}}`,
			installs: `{"version":2,"plugins":{"tinsel@red-professor":[{"scope":"user",` +
				`"installPath":"{cc1}/plugins/cache/red-professor/tinsel/1.0"}]}}`,
			rows:         []string{"  change  remove {cc1}/plugins/cache/professor/tinsel-red"},
			wantSettings: `{"enabledPlugins":{"tinsel@red-professor":true}}`,
			wantInstalls: `{"version":2,"plugins":{"tinsel@red-professor":[{"scope":"user",` +
				`"installPath":"{cc1}/plugins/cache/red-professor/tinsel/1.0"}]}}`,
			gaps:   []ClaudePluginGap{gap("tinsel-red@professor", "{cc1}/plugins/cache/professor/tinsel-red")},
			seed:   []string{"{store}/plugins/cache/professor/tinsel-red", "{store}/plugins/data/tinsel-red-professor"},
			remain: []string{"{store}/plugins/data/tinsel-red-professor"},
		},
		{
			name:     "a host holding no retired plugin is left as it is",
			ids:      []string{"tinsel@professor"},
			settings: `{"enabledPlugins":{"tinsel@other":true}}`, installs: `{"version":2,"plugins":{` + otherInstalls + `}}`,
			wantSettings: `{"enabledPlugins":{"tinsel@other":true}}`,
			wantInstalls: `{"version":2,"plugins":{` + otherInstalls + `}}`,
			seed:         append(slices.Clone(otherCopies), "{logs}/tinsel"),
			remain:       append(slices.Clone(otherCopies), "{logs}/tinsel"),
		},
		{
			name:     "an unreadable install record fails each dir and keeps every file",
			ids:      []string{"tinsel@professor"},
			settings: sharedSettings, installs: `{"plugins":`,
			rows: []string{
				"  FAIL    claude plugins in {cc1}: parse {cc1}/plugins/installed_plugins.json: unexpected end of JSON input",
				"  FAIL    claude plugins in {cc2}: parse {cc2}/plugins/installed_plugins.json: unexpected end of JSON input",
				"  FAIL    claude plugins in {store}: parse {store}/plugins/installed_plugins.json: unexpected end of JSON input",
			},
			wantErr:      true,
			wantSettings: sharedSettings, wantInstalls: `{"plugins":`,
			seed: ownCopies, remain: ownCopies,
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
			logs := filepath.Join(home, ".local", "state", "pfm", "plugin-logs")
			ownLogs := filepath.Join(root, "own-logs")
			managed := ManagedRoot(home)
			fill := strings.NewReplacer(
				"{store}",
				store,
				"{cc1}",
				cc1,
				"{cc2}",
				cc2,
				"{logs}",
				logs,
				"{ownlogs}",
				ownLogs,
			)
			settingsPath := filepath.Join(store, "settings.json")
			installsPath := filepath.Join(store, "plugins", "installed_plugins.json")
			writeFixture(t, settingsPath, fill.Replace(tc.settings))
			writeFixture(t, installsPath, fill.Replace(tc.installs))
			ledger := logs
			if tc.ledger != "" {
				ledger = fill.Replace(tc.ledger)
			}
			writeFixture(t, claudeEnvOwnershipPath(managed), `{"version":1,"files":{},"pluginLogDir":"`+ledger+`"}`)
			localPath := filepath.Join(cc2, "settings.local.json")
			if tc.local != "" {
				writeFixture(t, localPath, tc.local)
			}
			for _, path := range tc.seed {
				if strings.Contains(filepath.Base(path), ".jsonl") {
					writeFixture(t, fill.Replace(path), "{}\n")
					continue
				}
				writeFixture(t, filepath.Join(fill.Replace(path), "seed.json"), "{}")
			}
			for _, dir := range []string{cc1, cc2} {
				for _, entry := range []string{"settings.json", "plugins"} {
					writeLink(t, filepath.Join(store, entry), filepath.Join(dir, entry))
				}
			}
			if tc.dangling {
				if err := os.Remove(filepath.Join(cc2, "settings.json")); err != nil {
					t.Fatal(err)
				}
				dangling := filepath.Join(cc2, "settings.json")
				if err := os.Symlink(filepath.Join(root, "missing.json"), dangling); err != nil {
					t.Fatal(err)
				}
			}
			fillGaps := func(gaps []ClaudePluginGap) []ClaudePluginGap {
				var filled []ClaudePluginGap
				for _, gap := range gaps {
					filled = append(filled, ClaudePluginGap{Problem: fill.Replace(gap.Problem), Fix: gap.Fix})
				}
				return filled
			}
			assertGaps := func(when string, want []ClaudePluginGap) {
				t.Helper()
				got, gapErr := RetiredClaudePluginGaps(
					home, []string{cc1, cc2}, tc.ids, ClaudePluginBuild{Home: home, Plugins: tc.plugins},
				)
				if tc.gapErr != "" {
					if gapErr == nil || gapErr.Error() != fill.Replace(tc.gapErr) {
						t.Fatalf("gaps error %s = %v\nwant %s", when, gapErr, fill.Replace(tc.gapErr))
					}
				} else if (gapErr != nil) != tc.wantErr {
					t.Fatalf("gaps error %s = %v, want error %t", when, gapErr, tc.wantErr)
				}
				if !reflect.DeepEqual(got, fillGaps(want)) {
					t.Fatalf("gaps %s = %#v, %v\nwant %#v (error %t)", when, got, gapErr, fillGaps(want), tc.wantErr)
				}
			}
			if !tc.wantErr || tc.gapErr != "" {
				assertGaps("before the step", tc.gaps)
			}
			var output bytes.Buffer
			installer := &engine{apply: !tc.dryRun, managedRoot: managed, options: Options{
				Mode: ModeApply, Home: home, ConfigDir: store, PrimaryConfigDir: cc1,
				ClaudeAccounts: []pfmconfig.Account{{ID: 1, ConfigDir: cc1}, {ID: 2, ConfigDir: cc2}},
				Stdout:         &output, ClaudePlugins: tc.plugins,
				retired: &retired.Lists{ClaudePluginsRemoved: tc.ids},
			}}
			if tc.dryRun {
				installer.options.Mode = ModeDryRun
			}
			err = installer.retireClaudePlugins()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %t", err, tc.wantErr)
			}
			wantRows := fill.Replace(strings.Join(tc.rows, "\n"))
			if got := strings.TrimRight(output.String(), "\n"); got != wantRows {
				t.Fatalf("rows:\n%s\nwant:\n%s", got, wantRows)
			}
			assertRecord(t, settingsPath, fill.Replace(tc.wantSettings))
			assertRecord(t, installsPath, fill.Replace(tc.wantInstalls))
			if tc.local != "" {
				assertRecord(t, localPath, tc.wantLocal)
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
			if tc.wantErr || tc.dryRun {
				return
			}
			assertGaps("after the step", tc.gapsAfter)
			output.Reset()
			if err := installer.retireClaudePlugins(); err != nil {
				t.Fatalf("second run err = %v, want nil", err)
			}
			wantAgain := fill.Replace(strings.Join(tc.again, "\n"))
			if got := strings.TrimRight(output.String(), "\n"); got != wantAgain {
				t.Fatalf("second run rows:\n%s\nwant:\n%s", got, wantAgain)
			}
		})
	}
}
