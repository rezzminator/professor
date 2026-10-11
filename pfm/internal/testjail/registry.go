package testjail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// StageGlobalAgents links the checkout's machine-global agent sources into
// {home}/.claude/agents, the Claude half of what pfm install's engine-sync
// global step wires; a jail reading a role by name finds it there. The
// compiled Codex roles are engine-sync's, proven in its own gate.
func StageGlobalAgents(t *testing.T, home string) {
	t.Helper()
	source := filepath.Join(checkoutRoot(), "templates", "global", "agents")
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatalf("read the global agent sources %s: %v", source, err)
	}
	registry := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		target, link := filepath.Join(source, entry.Name()), filepath.Join(registry, entry.Name())
		if found, err := os.Readlink(link); err == nil && found == target {
			continue
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
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
