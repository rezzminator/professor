package codexgen

import (
	"bytes"
	"fmt"
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

func TestUnresolvableCommandDirectoryKeepsTwins(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	source := filepath.Join(root, ".claude", "commands", "lab")
	twin := filepath.Join(root, ".codex", "skills", "lab-run", "SKILL.md")
	seeded := seedUnresolvedFixture(t, root, home, filepath.Join(".claude", "commands", "lab", "run.md"), twin)
	old := filepath.Join(root, ".claude", "commands", "old.md")
	writeTestFile(t, old, "Old.\n")
	if result, err := Build(Options{Root: root, Home: home}); err != nil || !result.OK {
		t.Fatalf("seed old command: %#v, %v", result, err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(root, "saved-lab")); err != nil {
		t.Fatal(err)
	}
	target := "../../vendor/lab/commands"
	if err := os.Symlink(target, source); err != nil {
		t.Fatal(err)
	}
	result, err := Build(Options{Root: root, Home: home})
	if err != nil || !result.OK {
		t.Fatalf("build: %#v, %v", result, err)
	}
	if got := mustReadTestFile(t, twin); !bytes.Equal(got, seeded) {
		t.Fatalf("command directory twin changed: got %q, want %q", got, seeded)
	}
	warning := "source unresolvable: " + source + " → " + target + "; twin kept"
	if !contains(result.Warnings, warning) {
		t.Fatalf("warnings = %q, want %q", result.Warnings, warning)
	}
	if _, err := os.Lstat(filepath.Join(root, ".codex", "skills", "old")); !os.IsNotExist(err) {
		t.Fatalf("retired old command twin remains: %v", err)
	}
}

func TestUnresolvableSkillDirectoryKeepsTwin(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "Root.\n")
	source := filepath.Join(root, ".claude", "skills", "pdf")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "PDF.\n")
	if result, err := Build(Options{Root: root, Home: home}); err != nil || !result.OK {
		t.Fatalf("seed skill: %#v, %v", result, err)
	}
	twin := filepath.Join(root, ".codex", "skills", "pdf")
	seeded, err := os.Readlink(twin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(root, "saved-pdf")); err != nil {
		t.Fatal(err)
	}
	target := "../../vendor/pdf"
	if err := os.Symlink(target, source); err != nil {
		t.Fatal(err)
	}
	result, err := Build(Options{Root: root, Home: home})
	if err != nil || !result.OK {
		t.Fatalf("build: %#v, %v", result, err)
	}
	if got, err := os.Readlink(twin); err != nil || got != seeded {
		t.Fatalf("skill twin = %q, %v, want %q, nil", got, err, seeded)
	}
	if !contains(result.Warnings, "source unresolvable: "+source+" → "+target+"; twin kept") {
		t.Fatalf("warnings = %q, want named twin-kept warning", result.Warnings)
	}
}

func TestRealCommandSourceWinsOverKeptDirectoryTwin(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "Root.\n")
	command := filepath.Join(root, ".claude", "commands", "git-commit.md")
	writeTestFile(t, command, "Older body.\n")
	if result, err := Build(Options{Root: root, Home: home}); err != nil || !result.OK {
		t.Fatalf("seed command: %#v, %v", result, err)
	}
	writeTestFile(t, command, "Commit.\n")
	if err := os.Symlink("../../vendor/git/commands", filepath.Join(root, ".claude", "commands", "git")); err != nil {
		t.Fatal(err)
	}
	result, err := Build(Options{Root: root, Home: home})
	if err != nil || !result.OK {
		t.Fatalf("build: %#v, %v", result, err)
	}
	assertTestFileContains(t, filepath.Join(root, ".codex", "skills", "git-commit", "SKILL.md"), "Commit.")
	if containsFinding(result.Warnings, "twin kept") {
		t.Fatalf("real source reported as kept: %q", result.Warnings)
	}
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

func TestUnresolvableCommandFormsKeepAllTwins(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, shape := range []string{"file", "directory", "md-directory", "readme-directory", "skill-directory", "skill-file", "skill"} {
			t.Run(fmt.Sprintf("global_%v/%s", global, shape), func(t *testing.T) {
				root, home := t.TempDir(), t.TempDir()
				writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "Root.\n")
				base := root
				if global {
					base = home
				}
				name, leaf := "lab", "run.md"
				switch shape {
				case "file":
					name, leaf = "lab.md", ""
				case "md-directory":
					name = "lab.md"
				case "readme-directory":
					name = "README.md"
				case "skill-directory":
					name = "SKILL.md"
				case "skill", "skill-file":
					leaf = "SKILL.md"
				}
				source := filepath.Join(base, ".claude", "commands", name)
				path := source
				if leaf != "" {
					path = filepath.Join(source, leaf)
				}
				writeTestFile(t, path, "Run lab.\n")
				build := func() (Result, error) { return Build(Options{Root: root, Home: home}) }
				first, err := build()
				if err != nil || !first.OK {
					t.Fatalf("seed: %#v %v", first, err)
				}
				flat := "lab"
				if shape == "directory" {
					flat = "lab-run"
				}
				if shape == "md-directory" {
					flat = "lab.md-run"
				}
				if shape == "readme-directory" {
					flat = "README.md-run"
				}
				if shape == "skill-directory" {
					flat = "SKILL.md-run"
				}
				skillTwin := filepath.Join(base, ".codex", "skills", flat)
				before, err := os.Lstat(skillTwin)
				if err != nil {
					t.Fatal(err)
				}
				twin := skillTwin
				var seeded []byte
				if before.Mode()&os.ModeSymlink == 0 {
					twin = filepath.Join(twin, "SKILL.md")
					seeded = mustReadTestFile(t, twin)
				}
				if shape == "skill-file" {
					source = path
				}
				if err := os.Rename(source, filepath.Join(base, "saved-source")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../../missing-source", source); err != nil {
					t.Fatal(err)
				}
				got, err := build()
				if err != nil || !got.OK {
					t.Fatalf("build: %#v %v", got, err)
				}
				if _, err := os.Lstat(twin); err != nil {
					t.Fatalf("unresolved twin removed: %s: %v", twin, err)
				}
				if seeded != nil && !bytes.Equal(mustReadTestFile(t, twin), seeded) {
					t.Fatalf("twin rewritten: %s", twin)
				}
				if global && shape != "skill" && shape != "skill-file" {
					prompt := filepath.Join(home, ".codex", "prompts", flat+".md")
					if _, err := os.Stat(prompt); err != nil {
						t.Fatalf("prompt twin removed: %v", err)
					}
				}
				if !containsFinding(got.Warnings, "twin kept") {
					t.Fatalf("warnings: %q", got.Warnings)
				}
			})
		}
	}
}
