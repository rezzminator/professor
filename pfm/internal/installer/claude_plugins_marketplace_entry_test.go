package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
)

// claudeForeignSourceRefusal is what Claude Code prints refusing an add whose
// source kind differs from the marketplace's settings entry.
const claudeForeignSourceRefusal = `Cannot add marketplace "professor": its source doesn't match its ` +
	`extraKnownMarketplaces entry in user or managed settings; add it from the source that entry lists, ` +
	`or change the entry.`

// refuseForeignProfessorSource extends simulateClaude with what Claude Code
// does with settings on `plugin marketplace add`: an add whose source kind
// differs from the config dir's extraKnownMarketplaces.professor entry is
// refused and changes nothing; any other add records its source in that entry
// and in known_marketplaces.json.
func refuseForeignProfessorSource(t *testing.T, runner *pluginRunner) {
	t.Helper()
	simulateClaude(t, runner)
	directoryAdd := runner.onRun
	runner.fail = map[string]string{}
	runner.onRun = func(configDir, argv string) {
		fields := strings.Fields(argv)
		if len(fields) != 4 || strings.Join(fields[:3], " ") != "plugin marketplace add" {
			directoryAdd(configDir, argv)
			return
		}
		source := map[string]any{"source": claudeplugin.MarketplaceGitHub, "repo": fields[3]}
		if filepath.IsAbs(fields[3]) {
			source = map[string]any{"source": claudeplugin.MarketplaceDirectory, "path": fields[3]}
		}
		settingsPath := filepath.Join(configDir, "settings.json")
		var settings map[string]any
		if err := json.Unmarshal([]byte(readFixture(t, settingsPath)), &settings); err != nil {
			t.Errorf("simulated claude: parse %s: %v", settingsPath, err)
			return
		}
		entries, _ := settings[extraKnownMarketplacesKey].(map[string]any)
		entry, _ := entries["professor"].(map[string]any)
		if recorded, _ := entry["source"].(map[string]any); recorded != nil && recorded["source"] != source["source"] {
			runner.fail[configDir+" "+argv] = claudeForeignSourceRefusal
			return
		}
		if filepath.IsAbs(fields[3]) {
			directoryAdd(configDir, argv)
		} else {
			knownPath := filepath.Join(configDir, "plugins", "known_marketplaces.json")
			var known map[string]any
			if err := json.Unmarshal([]byte(readFixture(t, knownPath)), &known); err != nil {
				t.Errorf("simulated claude: parse %s: %v", knownPath, err)
				return
			}
			known["professor"] = map[string]any{"source": source}
			writeJSONFixture(t, knownPath, known)
		}
		if entry != nil {
			entry["source"] = source
			writeJSONFixture(t, settingsPath, settings)
		}
	}
}

func writeJSONFixture(t *testing.T, path string, document any) {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(raw))
}

// TestEnsureClaudePluginsSwitchesTheProfessorMarketplaceSource: a switch of
// claude.pluginSource either way, on an account whose settings record the
// professor marketplace from the other source, points that settings entry at
// the new source before the add Claude Code would otherwise refuse, through
// the account's settings.json link into the one file it names, keeping the
// entry's other keys, and runs no `plugin marketplace remove`; an entry
// already at the source is left as it is, and one in managed settings fails
// the pass naming its file, with no add run.
func TestEnsureClaudePluginsSwitchesTheProfessorMarketplaceSource(t *testing.T) {
	directory := func(path string) map[string]any { return map[string]any{"source": "directory", "path": path} }
	github := func(string) map[string]any {
		return map[string]any{"source": "github", "repo": "rezzminator/professor"}
	}
	for _, test := range []struct {
		name         string
		fromCheckout bool
		recorded     func(dev string) map[string]any
		managed      func(dev string) map[string]any
		want         func(dev string) map[string]any
		calls        func(dev string) []string
		change       string
		err          string
	}{
		{
			"directory to GitHub", false, directory, nil, github,
			func(string) []string { return []string{"plugin marketplace add rezzminator/professor"} },
			"  change  point extraKnownMarketplaces.professor in {first}/settings.json at rezzminator/professor: " +
				"it names {dev}, and Claude Code refuses an add from another source\n",
			"",
		},
		{
			"GitHub to directory", true, github, nil, directory,
			func(dev string) []string { return []string{"plugin marketplace add " + dev} },
			"  change  point extraKnownMarketplaces.professor in {first}/settings.json at {dev}: " +
				"it names rezzminator/professor, and Claude Code refuses an add from another source\n",
			"",
		},
		{
			"an entry with no source record", false, func(string) map[string]any { return map[string]any{} }, nil, github,
			func(string) []string { return []string{"plugin marketplace add rezzminator/professor"} },
			"  change  point extraKnownMarketplaces.professor in {first}/settings.json at rezzminator/professor: " +
				"it names no source, and Claude Code refuses an add from another source\n",
			"",
		},
		{
			"an entry already at the source is left as it is", false, github, nil, github,
			func(string) []string { return []string{} }, "", "",
		},
		{
			"an entry in managed settings fails naming its file", false, directory, directory, directory,
			func(string) []string { return []string{} }, "",
			"extraKnownMarketplaces.professor in managed settings {managed}/professor.json points at {dev}, " +
				"want rezzminator/professor: Claude Code refuses a professor marketplace add from another source " +
				"and pfm never writes managed settings — change or remove that entry, then run pfm install --yes",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			runner := &pluginRunner{}
			var out bytes.Buffer
			inst := pluginEngine(home, binary, first, second, runner, &out, true)
			inst.options.ClaudePluginsFromCheckout = test.fromCheckout
			inst.options.SourceRepo = writeMarketplaceClone(
				t,
				[]string{"forward-compaction"},
				[]string{"forward-compaction"},
			)
			inst.options.ClaudePlugins = bareList("forward-compaction")
			managed := filepath.Join(t.TempDir(), "managed-settings.d")
			inst.options.ManagedSettingsDir = managed
			dev := filepath.Join(home, ".local", "share", "professor-dev")
			replace := strings.NewReplacer("{first}", first, "{dev}", dev, "{managed}", managed)
			store := filepath.Join(home, ".claude", "settings.json")
			writeJSONFixture(t, store, map[string]any{
				"enabledPlugins": map[string]any{"forward-compaction@professor": true},
				extraKnownMarketplacesKey: map[string]any{
					"professor": map[string]any{"source": test.recorded(dev), "autoUpdate": true},
				},
			})
			link := filepath.Join(first, "settings.json")
			if err := os.Symlink(store, link); err != nil {
				t.Fatal(err)
			}
			writeInstalledPlugins(t, first, "forward-compaction@professor")
			writeJSONFixture(t, filepath.Join(first, "plugins", "known_marketplaces.json"),
				map[string]any{"professor": map[string]any{"source": test.recorded(dev)}})
			if test.managed != nil {
				writeJSONFixture(t, filepath.Join(managed, "professor.json"), map[string]any{
					extraKnownMarketplacesKey: map[string]any{"professor": map[string]any{"source": test.managed(dev)}},
				})
			}
			refuseForeignProfessorSource(t, runner)
			err := inst.ensureClaudePlugins(context.Background())
			if want := replace.Replace(test.err); want == "" && err != nil ||
				want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
				t.Fatalf("err = %v, want %q\n%s", err, want, out.String())
			}
			if got, want := pluginArgvs(runner), test.calls(dev); !reflect.DeepEqual(got, want) {
				t.Fatalf("calls\n got %q\nwant %q\n%s", got, want, out.String())
			}
			if want := replace.Replace(test.change); want == "" && strings.Contains(out.String(), "  change  point ") ||
				want != "" && !strings.Contains(out.String(), want) {
				t.Fatalf("output lacks change line %q:\n%s", want, out.String())
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(readFixture(t, store)), &settings); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"professor": map[string]any{"source": test.want(dev), "autoUpdate": true}}
			if !reflect.DeepEqual(settings[extraKnownMarketplacesKey], want) {
				t.Fatalf(
					"%s %s = %v, want %v",
					store,
					extraKnownMarketplacesKey,
					settings[extraKnownMarketplacesKey],
					want,
				)
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&fs.ModeSymlink == 0 {
				t.Fatalf("%s is no longer a link to %s: %v, %v", link, store, info, err)
			}
		})
	}
}
