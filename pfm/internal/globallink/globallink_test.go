package globallink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClassifyStates walks every state ClassifyLink must
// distinguish for a FILE-kind desired target: missing, an already-correct
// link, a same-shape copy (ours, safe to replace), a symlink stale but still
// inside the source repo (ours, safe to repoint), and a symlink pointing
// entirely outside the source repo (a conflict, never touched).
func TestClassifyStates(t *testing.T) {
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
		state, found, err := ClassifyLink(target, source, sourceRepo, File)
		if err != nil {
			t.Fatal(err)
		}
		if state != Missing || found != "" {
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
		state, _, err := ClassifyLink(target, source, sourceRepo, File)
		if err != nil {
			t.Fatal(err)
		}
		if state != Correct {
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
		state, found, err := ClassifyLink(target, source, sourceRepo, File)
		if err != nil {
			t.Fatal(err)
		}
		if state != Copy || found != "" {
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
		state, found, err := ClassifyLink(target, source, sourceRepo, File)
		if err != nil {
			t.Fatal(err)
		}
		if state != WrongTarget || found != stale {
			t.Fatalf("state=%s found=%q, want wrong-target/%q", state, found, stale)
		}
	})

	t.Run("dangling — link resolves to the desired source, but nothing exists there", func(t *testing.T) {
		goneSource := filepath.Join(sourceRepo, "generated", "gone.toml")
		target := filepath.Join(root, "dangling", "gone.toml")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(goneSource, target); err != nil {
			t.Fatal(err)
		}
		state, found, err := ClassifyLink(target, goneSource, sourceRepo, File)
		if err != nil {
			t.Fatal(err)
		}
		if state != Dangling || found != goneSource {
			t.Fatalf("state=%s found=%q, want dangling/%q", state, found, goneSource)
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
		state, found, err := ClassifyLink(target, source, sourceRepo, File)
		if err != nil {
			t.Fatal(err)
		}
		if state != Conflict || found != elsewhere {
			t.Fatalf("state=%s found=%q, want conflict/%q", state, found, elsewhere)
		}
	})

	t.Run("conflict type mismatch — a directory sitting where a file link belongs", func(t *testing.T) {
		target := filepath.Join(root, "type-mismatch", "alpha.md")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		state, _, err := ClassifyLink(target, source, sourceRepo, File)
		if err != nil {
			t.Fatal(err)
		}
		if state != Conflict {
			t.Fatalf("state=%s, want conflict — a directory must never be silently deleted", state)
		}
	})

	t.Run("dir-kind copy is replaceable, unlike a dir-kind conflict", func(t *testing.T) {
		target := filepath.Join(root, "dir-copy", "pcm")
		dirSource := filepath.Join(sourceRepo, "templates", "global", "skills", "pcm")
		writeTree(t, dirSource, map[string]string{"SKILL.md": "skill\n", "nested/a.md": "a\n"})
		writeTree(t, target, map[string]string{"SKILL.md": "skill\n", "nested/a.md": "a\n"})
		state, _, err := ClassifyLink(target, dirSource, sourceRepo, Dir)
		if err != nil {
			t.Fatal(err)
		}
		if state != Copy {
			t.Fatalf("state=%s, want copy for a byte-equal directory", state)
		}
	})
}

func TestClassifyAliasRoot(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, foundRoot, sourceRoot string }{
		{"alias target", alias, repo}, {"alias root", repo, alias},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(root, tc.name)
			stale := filepath.Join(tc.foundRoot, "stale.md")
			if err := os.Symlink(stale, target); err != nil {
				t.Fatal(err)
			}
			state, _, err := ClassifyLink(
				target, filepath.Join(tc.sourceRoot, "desired.md"), tc.sourceRoot, File,
			)
			if err != nil || state != WrongTarget {
				t.Fatalf("state=%s err=%v", state, err)
			}
		})
	}
}

// TestClassifyUnreadableIsAnErrorNeverMissing pins the rule this
// package exists to enforce: a probe that could not run must never render as
// "nothing there". A parent directory this process cannot traverse makes
// Lstat fail with something other than ErrNotExist, and that must surface as
// a genuine error, not Missing.
func TestClassifyUnreadableIsAnErrorNeverMissing(t *testing.T) {
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
	_, _, err := ClassifyLink(target, source, filepath.Join(root, "repo"), File)
	if err == nil {
		t.Fatal(
			"ClassifyLink reported no error for an unreadable target — an unreadable probe must never read as absence",
		)
	}
}

func TestApplyIsANoOpForCorrectAndConflict(t *testing.T) {
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
	if err := Apply(target, source, Conflict); err != nil {
		t.Fatalf("Apply(conflict): %v", err)
	}
	resolved, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != elsewhere {
		t.Fatalf("Apply(conflict) touched the target: now -> %s", resolved)
	}
}

func TestApplyRepointsADanglingLink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "generated", "alpha.toml")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "codex-agents", "alpha.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	// The link already resolves to the desired source, but nothing exists
	// there yet — the shape ClassifyLink reports Dangling for.
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	if err := Apply(target, source, Dangling); err != nil {
		t.Fatalf("Apply(dangling): %v", err)
	}
	resolved, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != source {
		t.Fatalf("Apply(dangling) = -> %s, want -> %s", resolved, source)
	}
}

func TestDescribeStateNamesConflictExactly(t *testing.T) {
	got := DescribeState(
		Conflict,
		"/home/x/.claude/agents/alpha.md",
		"/repo/templates/global/agents/alpha.md",
		"/home/x/elsewhere.md",
	)
	want := "CONFLICT /home/x/.claude/agents/alpha.md: not ours (points to /home/x/elsewhere.md)"
	if got != want {
		t.Fatalf("DescribeState = %q, want %q", got, want)
	}
}

func TestDescribeStateNamesDanglingByLinkName(t *testing.T) {
	got := DescribeState(
		Dangling,
		"/home/x/.codex/agents/alpha.toml",
		"/home/x/.local/state/pfm/generated/codex-agents/alpha.toml",
		"/home/x/.local/state/pfm/generated/codex-agents/alpha.toml",
	)
	if !strings.Contains(got, "DANGLING") || !strings.Contains(got, "/home/x/.codex/agents/alpha.toml") {
		t.Fatalf("DescribeState(dangling) = %q, want it to name DANGLING and the link path", got)
	}
}

func TestApplyKeepsPolicyLinkVisibleDuringRepoint(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	oldSource, newSource := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json")
	if err := os.Symlink(oldSource, target); err != nil {
		t.Fatal(err)
	}
	ready, stop, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	observations := make(chan error, 1)
	go func() {
		defer close(done)
		close(ready)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := os.Readlink(target); err != nil {
				select {
				case observations <- err:
				default:
				}
			}
		}
	}()
	<-ready
	for count := 0; count < 500; count++ {
		source := oldSource
		if count%2 == 0 {
			source = newSource
		}
		if err := Apply(target, source, WrongTarget); err != nil {
			close(stop)
			<-done
			t.Fatal(err)
		}
	}
	close(stop)
	<-done
	select {
	case err := <-observations:
		t.Fatalf("policy reader observed a missing link during repoint: %v", err)
	default:
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestClassifyRealEntryIsCopyOnlyWhenProven pins that a plain file or
// directory under a shipped name is ours to replace only when it is a proven
// copy of its source; anything else is a Conflict that Apply leaves alone.
func TestClassifyRealEntryIsCopyOnlyWhenProven(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	fileSource := filepath.Join(repo, "cmd.md")
	dirSource := filepath.Join(repo, "skill")
	writeTree(t, repo, map[string]string{"cmd.md": "shipped\n"})
	writeTree(t, dirSource, map[string]string{"SKILL.md": "shipped\n", "ref/a.md": "a\n"})
	cases := []struct {
		name   string
		kind   Kind
		source string
		files  map[string]string
		want   State
	}{
		{"edited file", File, fileSource, map[string]string{"": "mine\n"}, Conflict},
		{"equal file", File, fileSource, map[string]string{"": "shipped\n"}, Copy},
		{"user-built directory", Dir, dirSource, map[string]string{"SKILL.md": "mine\n"}, Conflict},
		{
			"edited file in directory",
			Dir,
			dirSource,
			map[string]string{"SKILL.md": "shipped\n", "ref/a.md": "b\n"},
			Conflict,
		},
		{
			"directory with an extra file",
			Dir,
			dirSource,
			map[string]string{"SKILL.md": "shipped\n", "ref/a.md": "a\n", "mine.md": "x\n"},
			Conflict,
		},
		{"directory missing a file", Dir, dirSource, map[string]string{"SKILL.md": "shipped\n"}, Conflict},
		{"equal directory", Dir, dirSource, map[string]string{"SKILL.md": "shipped\n", "ref/a.md": "a\n"}, Copy},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(base, "target", string(rune('a'+index)), "entry")
			if tc.kind == File {
				writeTree(t, filepath.Dir(target), map[string]string{"entry": tc.files[""]})
			} else {
				writeTree(t, target, tc.files)
			}
			state, _, err := ClassifyLink(target, tc.source, repo, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if state != tc.want {
				t.Fatalf("state=%s, want %s", state, tc.want)
			}
			if tc.want == Conflict {
				if err := Apply(target, tc.source, state); err != nil {
					t.Fatal(err)
				}
				if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("conflict was replaced: %v %v", info, err)
				}
			}
		})
	}
}

// TestApplyCopyReplacesByRenameNeverAbsent pins that replacing a proven copy
// (file or directory) with the link is a rename over the target: a reader
// polling the target while Apply runs never sees it missing.
func TestApplyCopyReplacesByRenameNeverAbsent(t *testing.T) {
	for _, kind := range []Kind{File, Dir} {
		base := t.TempDir()
		source := filepath.Join(base, "src")
		target := filepath.Join(base, "dst")
		if kind == File {
			writeTree(t, base, map[string]string{"src": "shipped\n"})
		} else {
			writeTree(t, source, map[string]string{"SKILL.md": "shipped\n"})
		}
		for i := 0; i < 300; i++ {
			if kind == File {
				writeTree(t, base, map[string]string{"dst": "shipped\n"})
			} else {
				writeTree(t, target, map[string]string{"SKILL.md": "shipped\n"})
			}
			stop := make(chan struct{})
			absent := make(chan error, 1)
			go func() {
				defer close(absent)
				for {
					select {
					case <-stop:
						return
					default:
					}
					if _, err := os.Lstat(target); err != nil {
						absent <- err
						return
					}
				}
			}()
			applyErr := Apply(target, source, Copy)
			close(stop)
			if err := <-absent; err != nil {
				t.Fatalf("kind %d: target absent during replacement: %v", kind, err)
			}
			if applyErr != nil {
				t.Fatal(applyErr)
			}
			if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("kind %d: target is not the link: %v %v", kind, info, err)
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
		}
	}
}
