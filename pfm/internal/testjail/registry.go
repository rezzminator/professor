package testjail

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
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

func globalAgentFixtures() ([]globalAgentFixture, error) {
	agentFixtureCache.Do(func() {
		python, err := deps.Resolve("python3")
		if err != nil {
			agentFixtureCache.err = fmt.Errorf("resolve agent inventory interpreter: %w", err)
			return
		}
		root := checkoutRoot()
		result, err := (deps.RealRunner{}).Run(context.Background(), []string{
			python,
			"-I",
			filepath.Join(root, "scripts", "agent-source-inventory.py"),
			filepath.Join(root, "templates", "global", "agents"),
		}, deps.RunOptions{})
		if err != nil {
			agentFixtureCache.err = fmt.Errorf(
				"read agent fixture inventory: %w: %s%s",
				err,
				result.Stderr,
				result.Stdout,
			)
			return
		}
		if result.ExitCode != 0 {
			agentFixtureCache.err = fmt.Errorf(
				"read agent fixture inventory: exit status %d: %s%s", result.ExitCode, result.Stderr, result.Stdout,
			)
			return
		}
		if err := json.Unmarshal(result.Stdout, &agentFixtureCache.rows); err != nil {
			agentFixtureCache.err = fmt.Errorf("decode agent fixture inventory: %w", err)
		}
	})
	return agentFixtureCache.rows, agentFixtureCache.err
}

// stagePython3 stages the python3 a healthy host has, the interpreter
// flights:tracer's tracetree kit runs on: a shim in shimDir, the one directory
// beyond the canonical bin the jail's PATH holds. The shim runs the
// interpreter the test process resolved; a host without one fails the test
// here, as the doctor check would fail it later.
func stagePython3(t *testing.T, shimDir string) {
	t.Helper()
	python, err := deps.Resolve("python3")
	if err != nil {
		t.Fatalf("the jail's healthy HOME needs python3 for the tracetree kit: %v", err)
	}
	if err := WriteExecutable(
		filepath.Join(shimDir, "python3"), []byte("#!/bin/sh\nexec '"+python+"' \"$@\"\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
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
