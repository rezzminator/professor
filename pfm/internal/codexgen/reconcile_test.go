package codexgen

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestReconcileFileDetectsModeOnlyDriftCheckNamesItBuildFixesIt pins L3-F14:
// content-only comparison (`if current == output.Content { r.Unchanged++
// }`) read a generated file at the wrong mode as "Unchanged". check must
// name the drift distinctly (never folded into STALE, since the content
// itself is already right), and build must fix it with a chmod rather than
// rewriting content that needed no rewrite.
func TestReconcileFileDetectsModeOnlyDriftCheckNamesItBuildFixesIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.toml")
	content := "name = \"fixture\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	output := generatedFile{Path: path, Content: content, Mode: 0o644}
	owns := func(string) bool { return true }

	check := &reconcileResult{}
	check.reconcileFile(output, ModeCheck, owns)
	if check.Unchanged != 0 {
		t.Fatalf("check.Unchanged=%d, want 0 — mode drift must not read as Unchanged", check.Unchanged)
	}
	if len(check.Problems) != 1 || !containsFinding(check.Problems, "MODE") || !containsFinding(check.Problems, path) {
		t.Fatalf("check.Problems=%#v, want exactly one MODE problem naming %s", check.Problems, path)
	}
	if got, err := os.Stat(path); err != nil || got.Mode().Perm() != 0o600 {
		t.Fatalf("check mode mutated the file: mode=%v err=%v", got, err)
	}

	build := &reconcileResult{}
	build.reconcileFile(output, ModeBuild, owns)
	if build.Wrote != 1 {
		t.Fatalf("build.Wrote=%d, want 1", build.Wrote)
	}
	if len(build.Problems) != 0 {
		t.Fatalf("build.Problems=%#v, want none", build.Problems)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("file mode after build=%v, want 0644", info.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("build rewrote content it did not need to: got=%q want=%q", got, content)
	}

	// A settled recheck reports Unchanged, never a second MODE problem.
	settled := &reconcileResult{}
	settled.reconcileFile(output, ModeCheck, owns)
	if settled.Unchanged != 1 || len(settled.Problems) != 0 {
		t.Fatalf("settled recheck = %#v, want Unchanged=1 and no problems", settled)
	}
}

// TestReconcileFileDefaultModeIsUnchangedFromBeforeTheFix pins the
// backward-compatible default: a generatedFile with Mode unset (every
// existing caller) behaves exactly as it did before L3-F14 — a file
// written at 0644 with matching content is Unchanged.
func TestReconcileFileDefaultModeIsUnchangedFromBeforeTheFix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.toml")
	content := "name = \"fixture\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result := &reconcileResult{}
	result.reconcileFile(generatedFile{Path: path, Content: content}, ModeCheck, func(string) bool { return true })
	if result.Unchanged != 1 || len(result.Problems) != 0 {
		t.Fatalf("result=%#v, want Unchanged=1 and no problems for a matching default-mode file", result)
	}
}

func TestRebuildableMirrorProblems(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "Root.\n")
	for _, name := range []string{"a", "b", "d"} {
		writeTestFile(t, filepath.Join(root, ".claude", "agents", name+".md"),
			"---\ndescription: Agent.\n---\nAgent.\n")
	}
	writeTestFile(t, filepath.Join(root, ".claude", "commands", "c.md"), "Command.\n")
	if result, err := Build(Options{Root: root, Home: home}); err != nil || !result.OK {
		t.Fatalf("seed build: %#v, %v", result, err)
	}
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "Edited root.\n")
	if err := os.Remove(filepath.Join(root, ".codex", "agents", "a.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, ".codex", "agents", "b.toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(root, ".codex", "skills", "c", "SKILL.md")
	writeTestFile(t, conflict, "hand\n")
	if err := os.Remove(filepath.Join(root, ".claude", "agents", "d.md")); err != nil {
		t.Fatal(err)
	}
	result, err := Check(Options{Root: root, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	wantProblems := []string{
		"STALE " + filepath.Join(root, "AGENTS.md"),
		"MISSING " + filepath.Join(root, ".codex", "agents", "a.toml"),
		"MODE " + filepath.Join(root, ".codex", "agents", "b.toml") + " (want 0644, have 0600)",
		"CONFLICT " + conflict + " — exists without a generated marker; not touching it",
		"ORPHAN " + filepath.Join(root, ".codex", "agents", "d.toml"),
	}
	if len(result.Problems) != len(wantProblems) {
		t.Fatalf("Problems = %q, want exactly %q", result.Problems, wantProblems)
	}
	for _, problem := range wantProblems {
		if !contains(result.Problems, problem) {
			t.Fatalf("Problems = %q, missing %q", result.Problems, problem)
		}
	}
	var wantRebuildable []string
	for _, problem := range result.Problems {
		if !strings.HasPrefix(problem, "CONFLICT ") {
			wantRebuildable = append(wantRebuildable, problem)
		}
	}
	if result.OK || !reflect.DeepEqual(result.Rebuildable, wantRebuildable) {
		t.Fatalf("check = %#v, want Problems %q and Rebuildable %q", result, wantProblems, wantRebuildable)
	}
}

func TestGlobalCommandsRebuildableOrphans(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".claude", "commands", "swap.md")
	writeTestFile(t, source, "Swap.\n")
	if result, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeBuild}); err != nil || !result.OK {
		t.Fatalf("seed global command: %#v, %v", result, err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	result, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeCheck})
	if err != nil || result.OK || len(result.Problems) == 0 || !reflect.DeepEqual(result.Rebuildable, result.Problems) {
		t.Fatalf("check = %#v, %v, want rebuildable orphan problems", result, err)
	}
	for _, problem := range result.Problems {
		if !strings.HasPrefix(problem, "ORPHAN "+filepath.Join(home, ".codex")+string(filepath.Separator)) {
			t.Fatalf("non-orphan problem: %s", problem)
		}
	}
}
