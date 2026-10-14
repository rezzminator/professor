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
