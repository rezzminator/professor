package codexgen

import (
	"os"
	"path/filepath"
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
