package fakegit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TestFakeGitKeepsStateBetweenAFastForwardAndAKeepReset states the model: a
// fast-forward to B then a keep reset to A leaves HEAD at A and the work tree
// holding A's files, and an argv outside the model is an error naming it.
func TestFakeGitKeepsStateBetweenAFastForwardAndAKeepReset(t *testing.T) {
	git := &Runner{}
	repo := t.TempDir()
	git.Must(t, repo, "init", "-q")
	git.Must(t, repo, "config", "user.email", "fixture.invalid")
	git.Must(t, repo, "config", "user.name", "fixture-identity")
	writeFakeGitFile(t, repo, "README.md", "a\n")
	git.Must(t, repo, "add", "README.md")
	git.Must(t, repo, "commit", "-qm", "A")
	git.Must(t, repo, "tag", "v1.0.0")
	commitA := git.Must(t, repo, "rev-parse", "HEAD")
	writeFakeGitFile(t, repo, "README.md", "b\n")
	writeFakeGitFile(t, repo, "NEXT", "b\n")
	git.Must(t, repo, "add", "-A")
	git.Must(t, repo, "commit", "-qm", "B")
	commitB := git.Must(t, repo, "rev-parse", "HEAD")
	git.Must(t, repo, "checkout", "-qb", "installed", "v1.0.0")

	git.Must(t, repo, "merge", "--ff-only", "--quiet", commitB)
	if got := git.Must(t, repo, "rev-parse", "HEAD"); got != commitB {
		t.Fatalf("after merge --ff-only, HEAD = %s, want B %s", got, commitB)
	}
	git.Must(t, repo, "reset", "--keep", commitA)

	if got := git.Must(t, repo, "rev-parse", "HEAD"); got != commitA {
		t.Fatalf("after reset --keep, HEAD = %s, want A %s", got, commitA)
	}
	if got := git.Must(t, repo, "symbolic-ref", "--short", "HEAD"); got != "installed" {
		t.Fatalf("branch = %q, want installed", got)
	}
	if got := fakeGitTree(t, repo); got != "README.md=a\n" {
		t.Fatalf("work tree = %q, want A's files alone (README.md=a)", got)
	}

	outsideRepository := t.TempDir()
	for _, row := range []struct {
		dir  string
		argv []string
	}{
		{repo, []string{"git", "rebase", "-i"}},
		{repo, []string{"git", "status", "--short"}},
		{repo, []string{"git", "status", "--porcelain", "--untracked-files=bogus"}},
		{repo, []string{"git", "status", "--porcelain", "--ignored"}},
		{repo, []string{"git", "checkout"}},
		{repo, []string{"git", "fetch"}},
		{repo, []string{"git", "worktree", "remove", filepath.Join(outsideRepository, "stage")}},
		{repo, []string{"git", "rev-parse", "--verify", "HEAD~1"}},
		{repo, []string{"git", "rev-parse", "--verify", "HEAD^"}},
		{repo, []string{"git", "rev-parse", "--verify", "@{u}"}},
		{repo, []string{"git", "log", "--format=%H", "HEAD"}},
		{repo, []string{"git", "log", "--format=%s", "..HEAD"}},
		{repo, []string{"git", "log", "--format=%s", "v1.0.0.."}},
		{repo, []string{"git", "log", "--format=%s", "v1.0.0...HEAD"}},
		{repo, []string{"go", "build"}},
		{outsideRepository, []string{"git", "rebase", "-i"}},
	} {
		_, err := git.Run(context.Background(), row.argv, deps.RunOptions{Dir: row.dir})
		want := fmt.Sprintf("fakegit: argv %q is outside the git model", row.argv)
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("Run(%q) in %s err = %v, want an error naming the argv", row.argv, row.dir, err)
		}
	}
}

// TestFakeGitRefusesAKeepResetOverALocalChange: reset --keep keeps a local
// change only to a file the two commits agree on; a file deleted from the
// disk alone is no change and comes back as the target holds it.
func TestFakeGitRefusesAKeepResetOverALocalChange(t *testing.T) {
	git := &Runner{}
	repo := t.TempDir()
	git.Must(t, repo, "init")
	git.Must(t, repo, "config", "user.email", "fixture.invalid")
	git.Must(t, repo, "config", "user.name", "fixture-identity")
	writeFakeGitFile(t, repo, "kept.txt", "same\n")
	writeFakeGitFile(t, repo, "moved.txt", "a\n")
	git.Must(t, repo, "add", ".")
	git.Must(t, repo, "commit", "-m", "A")
	commitA := git.Must(t, repo, "rev-parse", "HEAD")
	writeFakeGitFile(t, repo, "moved.txt", "b\n")
	git.Must(t, repo, "add", ".")
	git.Must(t, repo, "commit", "-m", "B")
	commitB := git.Must(t, repo, "rev-parse", "HEAD")
	for _, tc := range []struct {
		file    string
		deleted bool
		code    int
		tree    string
	}{
		{"moved.txt", false, 128, "kept.txt=same\n|moved.txt=local\n"},
		{"kept.txt", false, 0, "kept.txt=local\n|moved.txt=a\n"},
		{"moved.txt", true, 0, "kept.txt=same\n|moved.txt=a\n"},
	} {
		git.Must(t, repo, "reset", "--hard", commitB)
		writeFakeGitFile(t, repo, tc.file, "local\n")
		if tc.deleted {
			if err := os.Remove(filepath.Join(repo, tc.file)); err != nil {
				t.Fatal(err)
			}
		}
		result, err := git.Run(
			context.Background(),
			[]string{"git", "reset", "--keep", commitA},
			deps.RunOptions{Dir: repo},
		)
		if err != nil || result.ExitCode != tc.code || fakeGitTree(t, repo) != tc.tree {
			t.Errorf("local change to %s: exit %d (err %v), tree %q; want exit %d, tree %q",
				tc.file, result.ExitCode, err, fakeGitTree(t, repo), tc.code, tc.tree)
		}
	}
}

func writeFakeGitFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeGitTree renders a work tree outside .git as sorted "path=body" rows.
func fakeGitTree(t *testing.T, root string) string {
	t.Helper()
	var rows []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		rel, _ := filepath.Rel(root, path)
		rows = append(rows, filepath.ToSlash(rel)+"="+string(body))
		return err
	})
	if err != nil {
		t.Fatalf("read work tree %s: %v", root, err)
	}
	return strings.Join(rows, "|")
}

// newFakeGitRepo is an initialised model repository with the fixture identity.
func newFakeGitRepo(t *testing.T) (*Runner, string) {
	t.Helper()
	git, repo := &Runner{}, t.TempDir()
	git.Must(t, repo, "init", "-q")
	git.Must(t, repo, "config", "user.email", "fixture.invalid")
	git.Must(t, repo, "config", "user.name", "fixture-identity")
	return git, repo
}

// fakeGitRun runs git through the model and returns its result whatever the exit.
func fakeGitRun(t *testing.T, git *Runner, dir string, env []string, args ...string) deps.RunResult {
	t.Helper()
	result, err := git.Run(context.Background(), append([]string{"git"}, args...), deps.RunOptions{Dir: dir, Env: env})
	if err != nil {
		t.Fatalf("fake git %q in %s: %v", args, dir, err)
	}
	return result
}

// commitFakeGitFiles writes files, stages everything and commits it as subject.
func commitFakeGitFiles(t *testing.T, git *Runner, repo, subject string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		writeFakeGitFile(t, repo, rel, body)
	}
	git.Must(t, repo, "add", "-A")
	git.Must(t, repo, "commit", "-qm", subject)
	return git.Must(t, repo, "rev-parse", "HEAD")
}
