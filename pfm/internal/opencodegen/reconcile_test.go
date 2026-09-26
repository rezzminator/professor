package opencodegen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOpenCodeMarkerClaimsNewAndOldOutputs(t *testing.T) {
	for _, marker := range []string{newMarker, oldMarker} {
		if !hasMarker(marker + " from fixture") {
			t.Fatalf("marker %q was not claimable", marker)
		}
	}
}

func TestOpenCodeOrphanLinksOnlyClaimClaudeTargets(t *testing.T) {
	managed := t.TempDir()
	operatorTarget := filepath.Join(t.TempDir(), "operator-skill")
	if err := os.WriteFile(operatorTarget, []byte("operator\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	operatorLink := filepath.Join(managed, "operator-skill")
	if err := os.Symlink(operatorTarget, operatorLink); err != nil {
		t.Fatal(err)
	}

	claudeTarget := filepath.Join(t.TempDir(), ".claude", "skills", "owned-skill")
	if err := os.MkdirAll(filepath.Dir(claudeTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeTarget, []byte("compiler\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compilerLink := filepath.Join(managed, "owned-skill")
	if err := os.Symlink(claudeTarget, compilerLink); err != nil {
		t.Fatal(err)
	}

	result := reconcileResult{}
	reconcileOpenCodeOrphans(&result, managed, map[string]bool{}, ModeBuild)

	if _, err := os.Lstat(operatorLink); err != nil {
		t.Fatalf("operator symlink was removed: %v", err)
	}
	if _, err := os.Lstat(compilerLink); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("compiler-owned symlink was not reclaimed: err=%v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("deleted=%d, want 1", result.Deleted)
	}
	if strings.Contains(strings.Join(result.Problems, "\n"), "ORPHAN "+operatorLink) {
		t.Fatalf("operator symlink was reported as orphan: %#v", result.Problems)
	}
}

// TestReconcileOpenCodeFileDetectsModeOnlyDriftCheckNamesItBuildFixesIt pins
// L3-F14: content-only comparison read a generated file at the wrong mode
// as "Unchanged". check must name the drift distinctly, and build must fix
// it with a chmod rather than rewriting content that needed no rewrite.
func TestReconcileOpenCodeFileDetectsModeOnlyDriftCheckNamesItBuildFixesIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.md")
	content := newMarker + " from fixture\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	output := generatedFile{Path: path, Content: content, Mode: 0o644}

	check := &reconcileResult{}
	reconcileOpenCodeFile(check, output, ModeCheck)
	if check.Unchanged != 0 {
		t.Fatalf("check.Unchanged=%d, want 0 — mode drift must not read as Unchanged", check.Unchanged)
	}
	if len(check.Problems) != 1 || !strings.Contains(check.Problems[0], "MODE") ||
		!strings.Contains(check.Problems[0], path) {
		t.Fatalf("check.Problems=%#v, want exactly one MODE problem naming %s", check.Problems, path)
	}
	if got, err := os.Stat(path); err != nil || got.Mode().Perm() != 0o600 {
		t.Fatalf("check mutated the file: mode=%v err=%v", got, err)
	}

	build := &reconcileResult{}
	reconcileOpenCodeFile(build, output, ModeBuild)
	if build.Wrote != 1 || len(build.Problems) != 0 {
		t.Fatalf("build result Wrote=%d Problems=%#v, want Wrote=1 and no problems", build.Wrote, build.Problems)
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
}

// TestIsClaimableRefusesAPreExistingDifferingMirrorCopy pins L3-F19: a
// byte-for-byte MirrorCopy output (.opencode/LICENSE) used to be claimable
// unconditionally, so a hand-placed file at that exact path was silently
// overwritten with no CONFLICT the moment its content diverged from the
// source. With no marker a byte-copy can carry and no manifest of prior pfm
// output, isClaimable now treats any pre-existing content there exactly
// like an unrelated hand-placed file: never silently claimed.
func TestIsClaimableRefusesAPreExistingDifferingMirrorCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LICENSE")
	if err := os.WriteFile(path, []byte("an operator's own LICENSE, not ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if claimable, err := isClaimable(path); err != nil || claimable {
		t.Fatalf("isClaimable(%s) = %v, %v; want false for unmarked pre-existing content", path, claimable, err)
	}

	result := &reconcileResult{}
	reconcileOpenCodeFile(result, generatedFile{Path: path, Content: "the source LICENSE, different text\n"}, ModeBuild)
	if len(result.Problems) != 1 || !strings.Contains(result.Problems[0], "CONFLICT") {
		t.Fatalf("result.Problems=%#v, want exactly one CONFLICT", result.Problems)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "an operator's own LICENSE, not ours\n" {
		t.Fatalf("build overwrote the pre-existing file: %q", got)
	}
}

func TestReconcileOpenCodeFileNamesUnreadableStaleAndMissingOutputs(t *testing.T) {
	dir := t.TempDir()
	content := newMarker + " from fixture\n"

	missing := filepath.Join(dir, "missing.md")
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("a file, not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(blocker, "out.md")
	stale := filepath.Join(dir, "stale.md")
	if err := os.Symlink(filepath.Join(dir, ".claude", "commands", "stale.md"), stale); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		mode Mode
		want string
	}{
		{missing, ModeCheck, "MISSING " + missing},
		{unreadable, ModeCheck, "UNREADABLE " + unreadable + ": "},
		{unreadable, ModeBuild, "UNREADABLE " + unreadable + ": "},
		{stale, ModeCheck, "STALE " + stale},
		// An empty file carries no marker: pfm cannot prove it wrote it, so it
		// is a CONFLICT, never overwritten and never read as MISSING.
		{empty, ModeCheck, "CONFLICT " + empty},
	} {
		result := &reconcileResult{}
		reconcileOpenCodeFile(result, generatedFile{Path: tc.path, Content: content}, tc.mode)
		if len(result.Problems) != 1 || !strings.HasPrefix(result.Problems[0], tc.want) {
			t.Fatalf("mode %d %s: problems=%q, want one starting %q", tc.mode, tc.path, result.Problems, tc.want)
		}
	}
	if got, err := os.ReadFile(empty); err != nil || len(got) != 0 {
		t.Fatalf("empty output was touched: %q err=%v", got, err)
	}
}

func TestReconcileOpenCodeOrphansWarnsOnAnUnreadableEntry(t *testing.T) {
	managed := t.TempDir()
	skill := filepath.Join(managed, "odd-skill")
	if err := os.MkdirAll(filepath.Join(skill, "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := reconcileResult{}
	reconcileOpenCodeOrphans(&result, managed, map[string]bool{}, ModeBuild)

	want := "unreadable " + skill + " — cannot tell whether pfm owns it; left in place: "
	if len(result.Problems) != 0 || !containsProblem(result.Warnings, want) {
		t.Fatalf("warnings=%q problems=%q, want a warning %q", result.Warnings, result.Problems, want)
	}
	if _, err := os.Lstat(skill); err != nil {
		t.Fatalf("unreadable orphan was not left in place: %v", err)
	}
}

func TestReconcileOpenCodeLinkConcurrentReplaceNeverFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skill")
	output := generatedFile{Path: path, Link: filepath.Join("..", "..", ".claude", "skills", "skill")}
	for i := 0; i < 200; i++ {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "..", ".claude", "skills", "old"), path); err != nil {
			t.Fatal(err)
		}
		var results [2]reconcileResult
		var wg sync.WaitGroup
		for j := range results {
			wg.Add(1)
			go func(result *reconcileResult) {
				defer wg.Done()
				reconcileOpenCodeLink(result, output, ModeBuild)
			}(&results[j])
		}
		wg.Wait()
		for _, result := range results {
			if len(result.Problems) != 0 {
				t.Fatalf("round %d: concurrent link replace failed: %q", i, result.Problems)
			}
		}
		if got, err := os.Readlink(path); err != nil || got != output.Link {
			t.Fatalf("round %d: link=%q err=%v, want %q", i, got, err, output.Link)
		}
	}
}
