package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
// agent-effort -> effort-live-control; install ensures the successor and then
// sets each enabled retired id false (never uninstalls it), and a live chat
// defers the commands and that edit together.
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
			installed = installed || call.argv == "plugin install effort-live-control@effort-live-control -y"
		}
		if !installed {
			t.Fatalf("the successor was not installed: %v", runner.calls)
		}
		if line := "disable " + retiredGitHub + ", " + retiredDev + " in " + filepath.Join(first, "settings.json") +
			": retired, effort-live-control@effort-live-control replaces it"; !strings.Contains(out.String(), line) {
			t.Fatalf("no %q line:\n%s", line, out.String())
		}
	})
	t.Run("live chat defers", func(t *testing.T) {
		home, binary, first, second := pluginFixture(t)
		writeFixture(t, filepath.Join(first, "settings.json"), settings)
		proc := filepath.Join(home, "proc")
		if err := os.MkdirAll(filepath.Join(proc, "4242"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(first, "sessions", "4242.json"), "{}")
		runner := &pluginRunner{}
		var out bytes.Buffer
		inst := pluginEngine(home, binary, first, second, runner, &out, true)
		inst.options.ProcRoot = proc
		if err := inst.ensureClaudePlugins(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{retiredGitHub: true, retiredDev: true, "other@market": true}
		if got := readEnabled(t, first); !reflect.DeepEqual(got, want) || len(runner.calls) != 0 {
			t.Fatalf("enabledPlugins=%v calls=%v, want %v untouched", got, runner.calls, want)
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

// TestClaudePluginInstalledVersions: every distinct recorded version of an id,
// in record order; none for an id or a file with no record; a version that is
// not a string is skipped, never failing the record; an unparsable record is
// an error.
func TestClaudePluginInstalledVersions(t *testing.T) {
	configDir := t.TempDir()
	if versions, err := ClaudePluginInstalledVersions(configDir, "a@a-dev"); err != nil || versions != nil {
		t.Fatalf("no record file: versions=%v err=%v, want none", versions, err)
	}
	record := filepath.Join(configDir, "plugins", "installed_plugins.json")
	writeFixture(t, record, `{"version":2,"plugins":{"a@a-dev":[`+
		`{"scope":"user","version":"1.0.0"},{"scope":"project","version":"0.9.0"},`+
		`{"scope":"local","version":"1.0.0"},{"scope":"local"}]}}`)
	versions, err := ClaudePluginInstalledVersions(configDir, "a@a-dev")
	if err != nil || strings.Join(versions, ",") != "1.0.0,0.9.0" {
		t.Fatalf("versions=%v err=%v, want 1.0.0,0.9.0", versions, err)
	}
	if versions, err := ClaudePluginInstalledVersions(configDir, "b@b-dev"); err != nil || versions != nil {
		t.Fatalf("unrecorded id: versions=%v err=%v, want none", versions, err)
	}
	writeFixture(t, record, `{"plugins":{"a@a-dev":[{"version":7,"installPath":"/x"},{"version":"2.0.0"}]}}`)
	versions, err = ClaudePluginInstalledVersions(configDir, "a@a-dev")
	if err != nil || strings.Join(versions, ",") != "2.0.0" {
		t.Fatalf("non-string version: versions=%v err=%v, want 2.0.0 and the record still read", versions, err)
	}
	writeFixture(t, record, `{`)
	if _, err := ClaudePluginInstalledVersions(configDir, "a@a-dev"); err == nil ||
		!strings.HasPrefix(err.Error(), "parse "+record) {
		t.Fatalf("unparsable record err=%v, want a parse error naming %s", err, record)
	}
}
