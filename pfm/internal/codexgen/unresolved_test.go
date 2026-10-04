package codexgen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the twin of an unresolvable source: an adopter worktree
// whose .claude/agents/labber.md (or a repo command) is a symlink into an
// uninitialised submodule. The link names a source pfm cannot read right now,
// not a source the adopter retired, so build keeps the generated twin and
// says so; a source that is truly gone (no file, no link) still loses its
// twin to the orphan sweep.

const unresolvedSubmoduleTarget = "../../vendor/lab/agents/labber.md"

// seedUnresolvedFixture builds a repository whose source at rel compiles to
// twin, then returns the twin's bytes.
func seedUnresolvedFixture(t *testing.T, root, home, rel, twin string) []byte {
	t.Helper()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	writeTestFile(
		t,
		filepath.Join(root, rel),
		"---\nname: labber\ndescription: fixture lab role\nmodel: sonnet\n---\nRun the lab.\n",
	)
	if build, err := Build(Options{Root: root, Home: home}); err != nil || !build.OK {
		t.Fatalf("seed build: result=%#v err=%v", build, err)
	}
	seeded := mustReadTestFile(t, twin)
	if !generatedBytes(seeded) {
		t.Fatalf("seeded twin %s has no ownership marker", twin)
	}
	return seeded
}

// relinkToMissingTarget replaces the source at path with a symlink whose
// target does not exist, as an uninitialised submodule leaves it.
func relinkToMissingTarget(t *testing.T, path, target string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func assertUnresolvedTwinKept(t *testing.T, build Result, source, target, twin string, seeded []byte) {
	t.Helper()
	if !build.OK {
		t.Fatalf("build.OK = false, want true: an unresolvable source must not gate build: %#v", build)
	}
	have, err := os.ReadFile(twin)
	if err != nil {
		t.Fatalf("twin %s of the unresolvable source %s was removed: %v (result %#v)", twin, source, err, build)
	}
	if !bytes.Equal(have, seeded) {
		t.Fatalf("twin %s was rewritten:\nhave %q\nwant %q", twin, have, seeded)
	}
	want := "source unresolvable: " + source + " → " + target + "; twin kept"
	if !containsFinding(build.Warnings, want) {
		t.Fatalf("build warnings lack %q: %#v", want, build.Warnings)
	}
	if build.Deleted != 0 {
		t.Fatalf("build.Deleted = %d, want 0 with the twin kept: %#v", build.Deleted, build)
	}
}

func TestUnresolvableAgentSourceKeepsItsTwin(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	source := filepath.Join(root, ".claude", "agents", "labber.md")
	twin := filepath.Join(root, ".codex", "agents", "labber.toml")
	seeded := seedUnresolvedFixture(t, root, home, filepath.Join(".claude", "agents", "labber.md"), twin)
	relinkToMissingTarget(t, source, unresolvedSubmoduleTarget)

	build, err := Build(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	assertUnresolvedTwinKept(t, build, source, unresolvedSubmoduleTarget, twin, seeded)

	check, err := Check(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, problem := range check.Problems {
		if strings.HasPrefix(problem, "ORPHAN ") {
			t.Fatalf("check reports the kept twin as an orphan: %#v", check.Problems)
		}
	}
	if !containsFinding(check.Problems, "dangling source "+source) {
		t.Fatalf("check no longer gates on the dangling source: %#v", check.Problems)
	}
}

func TestUnresolvableRepoCommandSourceKeepsItsTwin(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	source := filepath.Join(root, ".claude", "commands", "lab", "run.md")
	twin := filepath.Join(root, ".codex", "skills", "lab-run", "SKILL.md")
	seeded := seedUnresolvedFixture(t, root, home, filepath.Join(".claude", "commands", "lab", "run.md"), twin)
	relinkToMissingTarget(t, source, unresolvedSubmoduleTarget)

	build, err := Build(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	assertUnresolvedTwinKept(t, build, source, unresolvedSubmoduleTarget, twin, seeded)
}

func TestAbsentAgentSourceStillDeletesItsTwin(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	source := filepath.Join(root, ".claude", "agents", "labber.md")
	twin := filepath.Join(root, ".codex", "agents", "labber.toml")
	seedUnresolvedFixture(t, root, home, filepath.Join(".claude", "agents", "labber.md"), twin)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	build, err := Build(Options{Root: root, Home: home})
	if err != nil || !build.OK {
		t.Fatalf("build: result=%#v err=%v", build, err)
	}
	if _, statErr := os.Lstat(twin); !os.IsNotExist(statErr) {
		t.Fatalf("twin %s of an absent source survived: %v", twin, statErr)
	}
	if build.Deleted != 1 {
		t.Fatalf("build.Deleted = %d, want 1: %#v", build.Deleted, build)
	}
	if containsFinding(build.Warnings, "twin kept") {
		t.Fatalf("an absent source claimed a kept twin: %#v", build.Warnings)
	}
}
