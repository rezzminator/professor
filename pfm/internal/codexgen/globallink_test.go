package codexgen

import (
	"os"
	"path/filepath"
	"testing"
)

// TestClassifyGlobalLinkStates walks every state ClassifyGlobalLink must
// distinguish for a FILE-kind desired target: missing, an already-correct
// link, a same-shape copy (ours, safe to replace), a symlink stale but still
// inside the source repo (ours, safe to repoint), and a symlink pointing
// entirely outside the source repo (a conflict, never touched).
func TestClassifyGlobalLinkStates(t *testing.T) {
	root := t.TempDir()
	sourceRepo := filepath.Join(root, "repo")
	source := filepath.Join(sourceRepo, "templates", "global", "agents", "alpha.md")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("agent body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("missing", func(t *testing.T) {
		target := filepath.Join(root, "missing", "alpha.md")
		state, found, err := ClassifyGlobalLink(target, source, sourceRepo, GlobalLinkFile)
		if err != nil {
			t.Fatal(err)
		}
		if state != GlobalLinkMissing || found != "" {
			t.Fatalf("state=%s found=%q, want missing/\"\"", state, found)
		}
	})

	t.Run("correct", func(t *testing.T) {
		target := filepath.Join(root, "correct", "alpha.md")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
		state, _, err := ClassifyGlobalLink(target, source, sourceRepo, GlobalLinkFile)
		if err != nil {
			t.Fatal(err)
		}
		if state != GlobalLinkCorrect {
			t.Fatalf("state=%s, want correct", state)
		}
	})

	t.Run("copy", func(t *testing.T) {
		target := filepath.Join(root, "copy", "alpha.md")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("agent body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		state, found, err := ClassifyGlobalLink(target, source, sourceRepo, GlobalLinkFile)
		if err != nil {
			t.Fatal(err)
		}
		if state != GlobalLinkCopy || found != "" {
			t.Fatalf("state=%s found=%q, want copy/\"\"", state, found)
		}
	})

	t.Run("wrong-target still in repo", func(t *testing.T) {
		stale := filepath.Join(sourceRepo, "templates", "global", "agents", "retired.md")
		if err := os.WriteFile(stale, []byte("stale\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "wrong-target", "alpha.md")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(stale, target); err != nil {
			t.Fatal(err)
		}
		state, found, err := ClassifyGlobalLink(target, source, sourceRepo, GlobalLinkFile)
		if err != nil {
			t.Fatal(err)
		}
		if state != GlobalLinkWrongTarget || found != stale {
			t.Fatalf("state=%s found=%q, want wrong-target/%q", state, found, stale)
		}
	})

	t.Run("conflict foreign symlink", func(t *testing.T) {
		elsewhere := filepath.Join(root, "elsewhere.md")
		if err := os.WriteFile(elsewhere, []byte("foreign\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "conflict", "alpha.md")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, target); err != nil {
			t.Fatal(err)
		}
		state, found, err := ClassifyGlobalLink(target, source, sourceRepo, GlobalLinkFile)
		if err != nil {
			t.Fatal(err)
		}
		if state != GlobalLinkConflict || found != elsewhere {
			t.Fatalf("state=%s found=%q, want conflict/%q", state, found, elsewhere)
		}
	})

	t.Run("conflict type mismatch — a directory sitting where a file link belongs", func(t *testing.T) {
		target := filepath.Join(root, "type-mismatch", "alpha.md")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		state, _, err := ClassifyGlobalLink(target, source, sourceRepo, GlobalLinkFile)
		if err != nil {
			t.Fatal(err)
		}
		if state != GlobalLinkConflict {
			t.Fatalf("state=%s, want conflict — a directory must never be silently deleted", state)
		}
	})

	t.Run("dir-kind copy is replaceable, unlike a dir-kind conflict", func(t *testing.T) {
		target := filepath.Join(root, "dir-copy", "deep-rr")
		if err := os.MkdirAll(filepath.Join(target, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		dirSource := filepath.Join(sourceRepo, "workflows", "deep-rr")
		state, _, err := ClassifyGlobalLink(target, dirSource, sourceRepo, GlobalLinkDir)
		if err != nil {
			t.Fatal(err)
		}
		if state != GlobalLinkCopy {
			t.Fatalf("state=%s, want copy for a same-shape directory", state)
		}
	})
}

// TestClassifyGlobalLinkUnreadableIsAnErrorNeverMissing pins the rule this
// package exists to enforce: a probe that could not run must never render as
// "nothing there". A parent directory this process cannot traverse makes
// Lstat fail with something other than ErrNotExist, and that must surface as
// a genuine error, not GlobalLinkMissing.
func TestClassifyGlobalLinkUnreadableIsAnErrorNeverMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits — this probe cannot be denied as root")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.MkdirAll(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	target := filepath.Join(blocked, "alpha.md")
	source := filepath.Join(root, "repo", "templates", "global", "agents", "alpha.md")
	_, _, err := ClassifyGlobalLink(target, source, filepath.Join(root, "repo"), GlobalLinkFile)
	if err == nil {
		t.Fatal(
			"ClassifyGlobalLink reported no error for an unreadable target — an unreadable probe must never read as absence",
		)
	}
}

func TestApplyGlobalLinkIsANoOpForCorrectAndConflict(t *testing.T) {
	root := t.TempDir()
	elsewhere := filepath.Join(root, "elsewhere.md")
	if err := os.WriteFile(elsewhere, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "alpha.md")
	if err := os.Symlink(elsewhere, target); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "repo", "templates", "global", "agents", "alpha.md")
	if err := ApplyGlobalLink(target, source, GlobalLinkConflict); err != nil {
		t.Fatalf("ApplyGlobalLink(conflict): %v", err)
	}
	resolved, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != elsewhere {
		t.Fatalf("ApplyGlobalLink(conflict) touched the target: now -> %s", resolved)
	}
}

func TestDescribeGlobalLinkStateNamesConflictExactly(t *testing.T) {
	got := DescribeGlobalLinkState(
		GlobalLinkConflict,
		"/home/x/.claude/agents/alpha.md",
		"/repo/templates/global/agents/alpha.md",
		"/home/x/elsewhere.md",
	)
	want := "CONFLICT /home/x/.claude/agents/alpha.md: not ours (points to /home/x/elsewhere.md)"
	if got != want {
		t.Fatalf("DescribeGlobalLinkState = %q, want %q", got, want)
	}
}
