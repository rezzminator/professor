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
			repair + "p-dev, then run pfm install --yes",
		}}},
		{"github for a local target", map[string]any{
			"p-dev": map[string]any{"source": "github", "repo": "owner/p"},
		}, []ClaudePluginGap{{
			"plugin p@p-dev marketplace p-dev in {file} points at owner/p, want /share/p-dev",
			repair + "p-dev, then run pfm install --yes",
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
