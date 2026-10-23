package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestClaudePluginGapsSparesListedKeys: a plugin enabled under two or more keys
// is named unless claude.plugins lists every one of them — pfm install would
// enable a disabled listed key again — and a key from the file prints with its
// control characters escaped.
func TestClaudePluginGapsSparesListedKeys(t *testing.T) {
	ours := ClaudePluginTarget{Name: "bar", ID: "bar@bar", GitHubID: "bar@bar", DevID: "bar@bar-dev", Own: true}
	theirs := ClaudePluginTarget{Name: "bar", ID: "bar@x", MarketplaceName: "x"}
	fooA := ClaudePluginTarget{Name: "foo", ID: "foo@a", MarketplaceName: "a"}
	fooB := ClaudePluginTarget{Name: "foo", ID: "foo@b", MarketplaceName: "b"}
	disable := "keep one copy: claude plugin disable each other key"
	for _, test := range []struct {
		name    string
		targets []ClaudePluginTarget
		enabled []string
		want    []ClaudePluginGap
	}{
		{"two listed full ids", []ClaudePluginTarget{fooA, fooB}, []string{"foo@a", "foo@b"}, nil},
		{"a bare name and a listed full id", []ClaudePluginTarget{ours, theirs}, []string{"bar@bar", "bar@x"}, nil},
		{"a listed id and a stranger", []ClaudePluginTarget{fooA}, []string{"foo@a", "foo@c"}, []ClaudePluginGap{{
			"plugin foo enabled twice (foo@a and foo@c) in {file}", disable,
		}}},
		{"a key with a control character", nil, []string{"baz@one", "baz@t\nwo"}, []ClaudePluginGap{{
			`plugin baz enabled twice (baz@one and baz@t\nwo) in {file}`, disable,
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "settings.json")
			enabled := map[string]bool{}
			for _, id := range test.enabled {
				enabled[id] = true
			}
			raw, _ := json.Marshal(map[string]any{"enabledPlugins": enabled})
			if err := os.WriteFile(file, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			gaps := claudePluginDuplicateGaps(mustSettings(t, file), file, test.targets)
			for index := range test.want {
				test.want[index].Problem = strings.ReplaceAll(test.want[index].Problem, "{file}", file)
			}
			if fmt.Sprint(gaps) != fmt.Sprint(test.want) {
				t.Fatalf("got %q want %q", gaps, test.want)
			}
		})
	}
}

func mustSettings(t *testing.T, file string) map[string]any {
	t.Helper()
	document, err := readClaudeSettingsDocument(file)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

// TestEnsureClaudePluginsDisablesTheRetiredAgentEffort: the plugin was renamed
// agent-effort -> auto-effort-control; install ensures the successor and then
// sets each enabled retired id false (never uninstalls it).
func TestEnsureClaudePluginsDisablesTheRetiredAgentEffort(t *testing.T) {
	const retiredGitHub, retiredDev = "agent-effort@agent-effort", "agent-effort@agent-effort-dev"
	settings := `{"enabledPlugins":{"` + retiredGitHub + `":true,"` + retiredDev + `":true,"other@market":true}}`
	readEnabled := func(t *testing.T, dir string) map[string]any {
		t.Helper()
		var document struct {
			Enabled map[string]any `json:"enabledPlugins"`
		}
		raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		return document.Enabled
	}
	t.Run("apply", func(t *testing.T) {
		home, binary, first, second := pluginFixture(t)
		writeFixture(t, filepath.Join(first, "settings.json"), settings)
		runner := &pluginRunner{}
		var out bytes.Buffer
		if err := pluginEngine(home, binary, first, second, runner, &out, true).ensureClaudePlugins(
			context.Background(),
		); err != nil {
			t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
		}
		want := map[string]any{retiredGitHub: false, retiredDev: false, "other@market": true}
		if got := readEnabled(t, first); !reflect.DeepEqual(got, want) {
			t.Fatalf("enabledPlugins=%v, want %v\n%s", got, want, out.String())
		}
		installed := false
		for _, call := range runner.calls {
			installed = installed || call.argv == "plugin install auto-effort-control@auto-effort-control -y"
		}
		if !installed {
			t.Fatalf("the successor was not installed: %v", runner.calls)
		}
		if line := "disable " + retiredGitHub + ", " + retiredDev + " in " + filepath.Join(first, "settings.json") +
			": retired, auto-effort-control@auto-effort-control replaces it"; !strings.Contains(out.String(), line) {
			t.Fatalf("no %q line:\n%s", line, out.String())
		}
	})
	t.Run("a retired id already false changes nothing", func(t *testing.T) {
		home, binary, first, second := pluginFixture(t)
		off := `{"enabledPlugins":{"` + retiredGitHub + `":false}}`
		writeFixture(t, filepath.Join(first, "settings.json"), off)
		var out bytes.Buffer
		if err := pluginEngine(home, binary, first, second, &pluginRunner{}, &out, true).ensureClaudePlugins(
			context.Background(),
		); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "disable agent-effort") {
			t.Fatalf("a disabled retired id was edited again:\n%s", out.String())
		}
	})
}

// TestEnsureClaudePluginsRefusesAListedRetiredName: claude.plugins still
// listing sub-agent-compact, the name forward-compaction retired, is a FAIL
// naming the successor, and the retired copy is never installed or enabled:
// ensuring it would enable what the successor's install disables. Doctor
// reports the same gap.
func TestEnsureClaudePluginsRefusesAListedRetiredName(t *testing.T) {
	const retired, successor = "sub-agent-compact", "forward-compaction"
	problem := "plugin " + retired + " is retired (now " + successor + ") but " + pfmconfig.KeyClaudePlugins + " lists it"
	home, binary, first, second := pluginFixture(t)
	settings := filepath.Join(first, "settings.json")
	writeFixture(t, settings, `{"enabledPlugins":{"sub-agent-compact@sub-agent-compact-dev":true}}`)
	runner := &pluginRunner{}
	var out bytes.Buffer
	inst := pluginEngine(home, binary, first, second, runner, &out, true)
	inst.options.ClaudePlugins = []pfmconfig.ClaudePlugin{{ID: retired}, {ID: successor}}
	err := inst.ensureClaudePlugins(context.Background())
	if err == nil || !strings.Contains(err.Error(), problem) {
		t.Fatalf("err = %v, want one naming %q\n%s", err, problem, out.String())
	}
	for _, call := range runner.calls {
		// The successor installs from the retired name's repository; only its ids are the retired copy's.
		if strings.Contains(call.argv, retired+"@") {
			t.Fatalf("the retired name was ensured: %q\n%s", call.argv, out.String())
		}
	}
	var document struct {
		Enabled map[string]any `json:"enabledPlugins"`
	}
	if err := json.Unmarshal([]byte(readFixture(t, settings)), &document); err != nil {
		t.Fatal(err)
	}
	for id, on := range document.Enabled {
		if strings.HasPrefix(id, retired+"@") && on != false {
			t.Fatalf("%s left enabled: %v\n%s", id, document.Enabled, out.String())
		}
	}
	targets := ClaudePluginTargets(ClaudePluginBuild{Plugins: []pfmconfig.ClaudePlugin{{ID: retired}}})
	gaps, err := ClaudePluginGaps(settings, targets)
	if err != nil {
		t.Fatal(err)
	}
	want := ClaudePluginGap{problem, "replace " + retired + " with " + successor + " in " +
		pfmconfig.KeyClaudePlugins + ", then " + ClaudePluginRepair}
	if !slices.Contains(gaps, want) || !ClaudePluginGapNames(want.Problem, retired) {
		t.Fatalf("gaps = %q, want %q among them", gaps, want)
	}
}

// TestEnsureClaudePluginsReleaseRetiresTheProfessorCopy: a release install
// ensures engine-sync@engine-sync and sets an enabled engine-sync@professor
// false, so only one copy's hooks run.
func TestEnsureClaudePluginsReleaseRetiresTheProfessorCopy(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	settings := filepath.Join(first, "settings.json")
	writeFixture(t, settings, `{"enabledPlugins":{"engine-sync@professor":true,"other@market":true}}`)
	var out bytes.Buffer
	if err := pluginEngine(home, binary, first, second, &pluginRunner{}, &out, true).ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	enabled, _ := mustSettings(t, settings)["enabledPlugins"].(map[string]any)
	if enabled["engine-sync@professor"] != false || enabled["other@market"] != true {
		t.Fatalf("enabledPlugins=%v, want engine-sync@professor false and other@market kept\n%s", enabled, out.String())
	}
	line := "disable engine-sync@professor in " + settings + ": engine-sync@engine-sync replaces it"
	if !strings.Contains(out.String(), line) {
		t.Fatalf("no %q line:\n%s", line, out.String())
	}
}

// TestClaudePluginGapsNamesEveryCopy: on an -alpha build a plugin of ours has
// three copies — GitHub, professor, -dev — and any two enabled is one gap; an
// install-only plugin enabled under any copy is a gap; a local copy whose
// content is not the checkout's, or cannot be read, is a gap. A plugin left
// right names nothing.
func TestClaudePluginGapsNamesEveryCopy(t *testing.T) {
	checkout := writePluginCheckout(t, t.TempDir(), "bar")
	marketplace := t.TempDir()
	mirror := filepath.Join(marketplace, "plugins", "bar")
	if err := mirrorPluginTree(checkout, mirror); err != nil {
		t.Fatal(err)
	}
	stale := t.TempDir()
	writeFixture(t, filepath.Join(stale, "plugins", "bar", ".claude-plugin", "plugin.json"), `{"name":"old"}`)
	dangling := t.TempDir()
	writeFixture(t, filepath.Join(dangling, "plugins", "bar", ".claude-plugin", "plugin.json"), `{"name":"bar"}`)
	if err := os.Symlink(
		filepath.Join(dangling, "gone"),
		filepath.Join(dangling, "plugins", "bar", "link"),
	); err != nil {
		t.Fatal(err)
	}
	_, danglingErr := pluginTreeDigest(filepath.Join(dangling, "plugins", "bar"), pluginCopySkipped)
	if danglingErr == nil {
		t.Fatal("a dangling link digested without an error")
	}
	target := func(market string, installOnly bool) ClaudePluginTarget {
		return ClaudePluginTarget{
			Name: "bar", ID: "bar@bar-dev", GitHubID: "bar@bar", ProfessorID: "bar@professor", DevID: "bar@bar-dev",
			Source: market, Local: true, Marketplace: market, Checkout: checkout, Own: true, InstallOnly: installOnly,
		}
	}
	for _, test := range []struct {
		name    string
		target  ClaudePluginTarget
		enabled []string
		want    []string
	}{
		{"enabled from its -dev copy alone", target(marketplace, false), []string{"bar@bar-dev"}, nil},
		{
			"enabled under three copies", target(marketplace, false),
			[]string{"bar@bar-dev", "bar@bar", "bar@professor"},
			[]string{"plugin bar enabled 3 times (bar@bar and bar@professor and bar@bar-dev) in {file}"},
		},
		{
			"its professor copy enabled instead", target(marketplace, false),
			[]string{"bar@professor"},
			[]string{"plugin bar@bar-dev not enabled in {file}"},
		},
		{"install-only and disabled", target(marketplace, true), nil, nil},
		{
			"install-only and enabled", target(marketplace, true),
			[]string{"bar@bar-dev"},
			[]string{"plugin bar enabled (bar@bar-dev) in {file}, but claude.plugins does not list it"},
		},
		{
			"a copy that differs from its checkout", target(stale, false),
			[]string{"bar@bar-dev"},
			[]string{"plugin bar@bar-dev copy " + filepath.Join(stale, "plugins", "bar") + " differs from " + checkout},
		},
		{
			"a copy that cannot be read", target(dangling, false),
			[]string{"bar@bar-dev"},
			[]string{"plugin bar@bar-dev copy " + filepath.Join(dangling, "plugins", "bar") + " cannot be compared with " +
				checkout + ": " + danglingErr.Error()},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "settings.json")
			enabled := map[string]bool{}
			for _, id := range test.enabled {
				enabled[id] = true
			}
			raw, _ := json.Marshal(map[string]any{"enabledPlugins": enabled})
			if err := os.WriteFile(file, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			gaps, err := ClaudePluginGaps(file, []ClaudePluginTarget{test.target})
			var got []string
			for _, gap := range gaps {
				got = append(got, gap.Problem)
				if gap.Fix != ClaudePluginRepair {
					t.Fatalf("gap %q fix %q, want %q", gap.Problem, gap.Fix, ClaudePluginRepair)
				}
			}
			for index := range test.want {
				test.want[index] = strings.ReplaceAll(test.want[index], "{file}", file)
			}
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %q, %v want %q", got, err, test.want)
			}
		})
	}
}

// TestEnsureClaudePluginsSettlesTheRetiredPluginConfigs: install moves a
// retired plugin's pluginConfigs entry to its successor and deletes every
// retired and other-copy entry, so Claude Code's plugin finds no duplicate to
// warn on; a successor's own entry is never overwritten, and a dry run writes
// nothing.
func TestEnsureClaudePluginsSettlesTheRetiredPluginConfigs(t *testing.T) {
	const name = "auto-effort-control"
	successor, github, professor := devID(name), name+"@"+name, name+"@professor"
	retiredGitHub, retiredDev := "effort-live-control@effort-live-control", "effort-live-control@effort-live-control-dev"
	patterns := `{"options":{"testCommandPatterns":["make test"]}}`
	other := `{"options":{"testCommandPatterns":["go test"]}}`
	for _, test := range []struct {
		name      string
		configs   string
		apply     bool
		wantKeys  map[string]string
		wantLines []string
	}{
		{
			"two retired entries and no successor entry", `"` + retiredGitHub + `":` + patterns + `,"` + retiredDev + `":` + patterns,
			true,
			map[string]string{successor: patterns},
			[]string{
				"; copy the pluginConfigs of " + retiredGitHub + " to " + successor +
					"; drop the pluginConfigs of " + retiredGitHub + ", " + retiredDev + "\n",
			},
		},
		{
			"the first retired entry is carried, a differing one named", `"` + retiredGitHub + `":` + patterns + `,"` +
				retiredDev + `":` + other,
			true,
			map[string]string{successor: patterns},
			[]string{"; drop the pluginConfigs of " + retiredGitHub + ", " + retiredDev + " (differs)\n"},
		},
		{
			"a successor entry is never overwritten", `"` + successor + `":` + other + `,"` + retiredGitHub + `":` + patterns,
			true,
			map[string]string{successor: other},
			[]string{"; drop the pluginConfigs of " + retiredGitHub + " (differs)\n"},
		},
		{
			"another copy's entry is dropped after it is carried", `"` + github + `":` + patterns + `,"` + professor + `":` + patterns,
			true,
			map[string]string{successor: patterns},
			[]string{"; drop the pluginConfigs of " + github + ", " + professor + "\n"},
		},
		{"a dry run writes nothing", `"` + retiredGitHub + `":` + patterns, false, nil, []string{
			"; copy the pluginConfigs of " + retiredGitHub + " to " + successor + "; drop the pluginConfigs of " + retiredGitHub + "\n",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			inst, runner, out, _, first := alphaPluginEngine(t, root)
			writePluginCheckout(t, root, name)
			inst.apply = test.apply
			inst.options.ClaudePlugins = []pfmconfig.ClaudePlugin{{ID: name}}
			simulateClaude(t, runner)
			// Retired ids already disabled: only their pluginConfigs are stale.
			before := `{"enabledPlugins":{"` + retiredGitHub + `":false,"` + retiredDev + `":false,"` + github + `":false},` +
				`"pluginConfigs":{` + test.configs + `}}`
			path := filepath.Join(first, "settings.json")
			writeFixture(t, path, before)
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
			}
			for _, line := range test.wantLines {
				if !strings.Contains(out.String(), line) {
					t.Fatalf("missing %q:\n%s", line, out.String())
				}
			}
			after := readFixture(t, path)
			if !test.apply {
				if after != before {
					t.Fatalf("dry run wrote settings: %s", after)
				}
				return
			}
			var got struct {
				Configs map[string]json.RawMessage `json:"pluginConfigs"`
			}
			if err := json.Unmarshal([]byte(after), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Configs) != len(test.wantKeys) {
				t.Fatalf("pluginConfigs = %s, want only %v", after, test.wantKeys)
			}
			for id, want := range test.wantKeys {
				var gotEntry, wantEntry any
				if err := json.Unmarshal(got.Configs[id], &gotEntry); err != nil {
					t.Fatalf("pluginConfigs[%s] = %s: %v", id, got.Configs[id], err)
				}
				if err := json.Unmarshal([]byte(want), &wantEntry); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(gotEntry, wantEntry) {
					t.Fatalf("pluginConfigs[%s] = %s, want %s", id, got.Configs[id], want)
				}
			}
			out.Reset()
			err := inst.ensureClaudePlugins(context.Background())
			if err != nil || strings.Contains(out.String(), "pluginConfigs") {
				t.Fatalf("second run err=%v:\n%s", err, out.String())
			}
		})
	}
}

// TestClaudePluginGapsNamesAStaleRetiredPluginConfig: doctor reports a
// pluginConfigs entry of a retired or other copy beside its successor's.
func TestClaudePluginGapsNamesAStaleRetiredPluginConfig(t *testing.T) {
	const name = "auto-effort-control"
	target := ClaudePluginTarget{
		Name: name, ID: devID(name), DevID: devID(name), GitHubID: name + "@" + name, Own: true,
	}
	file := filepath.Join(t.TempDir(), "settings.json")
	writeFixture(t, file, `{"enabledPlugins":{"`+target.ID+`":true},"pluginConfigs":{"`+target.ID+`":{"options":{}},`+
		`"effort-live-control@effort-live-control":{"options":{}},"`+target.GitHubID+`":{"options":{}}}}`)
	gaps, err := ClaudePluginGaps(file, []ClaudePluginTarget{target})
	want := ClaudePluginGap{
		"pluginConfigs of " + target.GitHubID + " and effort-live-control@effort-live-control still in " + file +
			": retired or other copies of " + target.ID,
		"pfm install copies the first to " + target.ID + " when it has none, then drops them",
	}
	if err != nil || len(gaps) != 1 || gaps[0] != want {
		t.Fatalf("gaps = %q, %v; want [%q]", gaps, err, want)
	}
	writeFixture(t, file, `{"enabledPlugins":{"`+target.ID+`":true},"pluginConfigs":{"`+target.ID+`":{"options":{}}}}`)
	if gaps, err = ClaudePluginGaps(file, []ClaudePluginTarget{target}); err != nil || len(gaps) != 0 {
		t.Fatalf("a settled settings.json reported %q, %v", gaps, err)
	}
}
