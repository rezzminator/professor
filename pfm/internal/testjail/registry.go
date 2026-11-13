package testjail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// StageGlobalAgents links every original agent from the checkout's
// templates/global/agents and every declaration from its tracked
// templates/generated/agents render, the links engine-sync global writes. Like
// the jail's fake engine-sync these are fixtures, not a second compiler:
// engine-sync's gate proves the rendered manuals and settings. Shared files
// below a subdirectory are never agents: the inventory never lists them. It
// also links the checkout's global rules, the other registry a healthy home holds.
func StageGlobalAgents(t *testing.T, home string) {
	t.Helper()
	stageGlobalRules(t, home)
	rows, err := globalAgentFixtures()
	if err != nil {
		t.Fatalf("inventory global agent fixtures: %v", err)
	}
	registry := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatalf("create agent fixture directory %s: %v", registry, err)
	}
	for _, row := range rows {
		target := row.Path
		if row.Kind == "declaration" {
			target = filepath.Join(checkoutRoot(), "templates", "generated", "agents", row.Name+".md")
			if _, err := os.Stat(target); err != nil {
				t.Fatalf(
					"declaration %s has no tracked render at %s (run engine-sync global): %v",
					row.Name,
					target,
					err,
				)
			}
		}
		link := filepath.Join(registry, row.Name+".md")
		if found, err := os.Readlink(link); err == nil && found == target {
			continue
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("link agent fixture %s -> %s: %v", link, target, err)
		}
	}
}

// stageGlobalRules links every *.md of the checkout's templates/global/rules
// into {home}/.claude/rules, the links pfm install makes, so a healthy jail
// home passes doctor's rules row; StageGlobalAgents calls it.
func stageGlobalRules(t *testing.T, home string) {
	t.Helper()
	source := filepath.Join(checkoutRoot(), "templates", "global", "rules")
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatalf("inventory global rule fixtures %s: %v", source, err)
	}
	registry := filepath.Join(home, ".claude", "rules")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatalf("create rule fixture directory %s: %v", registry, err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		link := filepath.Join(registry, entry.Name())
		if err := os.Symlink(filepath.Join(source, entry.Name()), link); err != nil && !os.IsExist(err) {
			t.Fatalf("link rule fixture %s: %v", link, err)
		}
	}
}

type globalAgentFixture struct{ Name, Path, Kind, Role string }

var agentFixtureCache struct {
	sync.Once
	rows []globalAgentFixture
	err  error
}

// globalAgentFixtures reads the agent inventory of templates/global/agents from the tracked
// testdata/global-agents.json, each row's path joined to this checkout; it starts nothing. The
// fence gate's fixture step holds the file equal to a fresh run of the inventory script.
func globalAgentFixtures() ([]globalAgentFixture, error) {
	agentFixtureCache.Do(func() {
		fixture := filepath.Join(checkoutRoot(), "pfm", "internal", "testjail", "testdata", "global-agents.json")
		body, err := os.ReadFile(fixture)
		if err != nil {
			agentFixtureCache.err = fmt.Errorf("read agent fixture inventory %s: %w", fixture, err)
			return
		}
		var rows []globalAgentFixture
		if err := json.Unmarshal(body, &rows); err != nil {
			agentFixtureCache.err = fmt.Errorf("decode agent fixture inventory %s: %w", fixture, err)
			return
		}
		for index := range rows {
			rows[index].Path = filepath.Join(checkoutRoot(), rows[index].Path)
		}
		agentFixtureCache.rows = rows
	})
	return agentFixtureCache.rows, agentFixtureCache.err
}

// tracetreeKitStub prints the usage line the shipped kit prints when it is
// given no subcommand. The shipped kit lives in the flights plugin, whose
// checkout a Professor test never reads, so a staged plugin folder carries
// this stand-in; the flights repo's own tests run the real kit.
const tracetreeKitStub = "import sys\nsys.exit(\"usage: tracetree.py <command> ...\")\n"

// stagedPluginPath is the installPath StageClaudePlugins records for id.
func stagedPluginPath(store, id string) string { return filepath.Join(store, "plugins", "cache", id) }

// StageTracetreeKit places the tracetree kit in the folder StageClaudePlugins
// staged for every flights plugin among ids, where pfm install's copy puts it:
// {folder}/skills/tracetree/tracetree.py. ids holding no flights plugin fail
// the test.
func StageTracetreeKit(t *testing.T, store string, ids []string) {
	t.Helper()
	staged := 0
	for _, id := range ids {
		if plugin, _, _ := strings.Cut(id, "@"); plugin != "flights" {
			continue
		}
		dir := filepath.Join(stagedPluginPath(store, id), "skills", "tracetree")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tracetree.py"), []byte(tracetreeKitStub), 0o644); err != nil {
			t.Fatal(err)
		}
		staged++
	}
	if staged == 0 {
		t.Fatalf("plugin ids %v hold no flights plugin to stage the tracetree kit for", ids)
	}
}
