package testjail

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// StageGlobalAgents links original agents and stages generated stand-ins for
// declarations. Like the jail's fake engine-sync, these are fixtures, not a
// second compiler: engine-sync's gate proves the rendered manuals and settings.
// Abstract layers never enter the registry.
func StageGlobalAgents(t *testing.T, home string) {
	t.Helper()
	rows, err := globalAgentFixtures()
	if err != nil {
		t.Fatalf("inventory global agent fixtures: %v", err)
	}
	registry := filepath.Join(home, ".claude", "agents")
	generated := paths.GeneratedClaudeAgentsDir(home)
	for _, dir := range []string{registry, generated} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create agent fixture directory %s: %v", dir, err)
		}
	}
	for _, row := range rows {
		if row.Kind == "abstract" {
			continue
		}
		target := row.Path
		if row.Kind == "declaration" {
			target = filepath.Join(generated, row.Name+".md")
			role := ""
			if row.Role != "" {
				role = "role: " + row.Role + "\n"
			}
			data := "---\nname: " + row.Name + "\n" + role + "description: Generated jail agent fixture.\n---\n\nFixture constitution for " + row.Name + ".\n"
			if err := os.WriteFile(target, []byte(data), 0o644); err != nil {
				t.Fatalf("write declaration fixture %s: %v", target, err)
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

// stageTracerIntakeKit stages what the tracer-intake launch hook needs on a
// healthy host: the checkout's tracetree skill linked at
// {home}/.claude/skills/tracetree, the link pfm install makes, and a python3 in
// shimDir, the one directory beyond the canonical bin the jail's PATH holds.
// The shim runs the interpreter the test process resolved; a host without one
// fails the test here, as the doctor check would fail it later.
func stageTracerIntakeKit(t *testing.T, home, shimDir string) {
	t.Helper()
	python, err := deps.Resolve("python3")
	if err != nil {
		t.Fatalf("the jail's healthy HOME needs python3 for the tracer-intake hook: %v", err)
	}
	if err := WriteExecutable(
		filepath.Join(shimDir, "python3"), []byte("#!/bin/sh\nexec '"+python+"' \"$@\"\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(checkoutRoot(), "templates", "global", "skills", "tracetree")
	if err := os.Symlink(source, filepath.Join(skills, "tracetree")); err != nil {
		t.Fatal(err)
	}
}
