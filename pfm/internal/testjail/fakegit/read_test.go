package fakegit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TestFakeGitDescribeAndAncestry: describe names the nearest tag, an annotated
// one first on a shared commit, and fails with no tag; merge-base answers
// --is-ancestor by exit code.
func TestFakeGitDescribeAndAncestry(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	if result := fakeGitRun(
		t,
		git,
		repo,
		[]string{},
		"describe",
		"--tags",
		"--abbrev=0",
		"HEAD",
	); result.ExitCode != 128 {
		t.Fatalf("describe on an unborn branch: exit %d, want 128", result.ExitCode)
	}
	first := commitFakeGitFiles(t, git, repo, "A", map[string]string{"a": "a\n"})
	if result := fakeGitRun(
		t,
		git,
		repo,
		[]string{},
		"describe",
		"--tags",
		"--abbrev=0",
		"HEAD",
	); result.ExitCode != 128 {
		t.Fatalf("describe with no tag: exit %d, want 128", result.ExitCode)
	}
	git.Must(t, repo, "tag", "v0.9.0")
	git.Must(t, repo, "tag", "-a", "-m", "nightly", "nightly")
	second := commitFakeGitFiles(t, git, repo, "B", map[string]string{"a": "b\n"})
	git.Must(t, repo, "tag", "v0.10.0")
	for rev, want := range map[string]string{first: "nightly", second: "v0.10.0"} {
		if got := git.Must(t, repo, "describe", "--tags", "--abbrev=0", rev); got != want {
			t.Errorf("describe %s = %q, want %q", rev, got, want)
		}
	}
	for _, tc := range []struct {
		ancestor, descendant string
		code                 int
	}{{first, second, 0}, {second, first, 1}, {second, second, 0}} {
		result := fakeGitRun(t, git, repo, []string{}, "merge-base", "--is-ancestor", tc.ancestor, tc.descendant)
		if result.ExitCode != tc.code {
			t.Errorf(
				"merge-base --is-ancestor %s %s: exit %d, want %d",
				tc.ancestor,
				tc.descendant,
				result.ExitCode,
				tc.code,
			)
		}
	}
	if got := git.Must(t, repo, "tag", "--list"); got != "nightly\nv0.10.0\nv0.9.0" {
		t.Fatalf("tag --list = %q, want the three tags sorted", got)
	}
}

// TestFakeGitShowReadsACommitsFile: show prints the bytes a commit holds and
// refuses a path the commit lacks in git's two wordings, on disk or not.
func TestFakeGitShowReadsACommitsFile(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	commitFakeGitFiles(t, git, repo, "A", map[string]string{"pins/a.md": "pinned\n"})
	writeFakeGitFile(t, repo, "pins/b.md", "untracked\n")
	for _, row := range []struct {
		spec, stdout, stderr string
		exit                 int
	}{
		{"HEAD:pins/a.md", "pinned\n", "", 0},
		{"HEAD:pins/b.md", "", "fatal: path 'pins/b.md' exists on disk, but not in 'HEAD'\n", 128},
		{"HEAD:pins/c.md", "", "fatal: path 'pins/c.md' does not exist in 'HEAD'\n", 128},
		{"v9:pins/a.md", "", "fatal: invalid object name 'v9'.\n", 128},
	} {
		result := fakeGitRun(t, git, repo, []string{}, "show", row.spec)
		if string(result.Stdout) != row.stdout || string(result.Stderr) != row.stderr || result.ExitCode != row.exit {
			t.Errorf("show %s = exit %d stdout %q stderr %q; want exit %d stdout %q stderr %q", row.spec,
				result.ExitCode, result.Stdout, result.Stderr, row.exit, row.stdout, row.stderr)
		}
	}
	if _, err := git.Run(
		context.Background(),
		[]string{"git", "show", "HEAD:pins"},
		deps.RunOptions{Dir: repo},
	); err == nil {
		t.Fatal("show of a directory ran; want an error naming it outside the model")
	}
}

// TestFakeGitRevParseAndHashObjectCompareABlobToTheDisk: the two reads the engine guard
// compares: rev-parse --verify -q HEAD:./{file} from a subdirectory prints the committed blob id,
// a directory's tree id, and refuses a path leaving the work tree; hash-object prints the disk
// file's, a symlink's target's; a file HEAD does not hold answers exit 1 and nothing.
func TestFakeGitRevParseAndHashObjectCompareABlobToTheDisk(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	commitFakeGitFiles(t, git, repo, "A", map[string]string{"scripts/gate.sh": "hello\n"})
	writeFakeGitFile(t, repo, "scripts/edited.sh", "new\n")
	dir := filepath.Join(repo, "scripts")
	if err := os.Symlink("gate.sh", filepath.Join(dir, "link.sh")); err != nil {
		t.Fatal(err)
	}
	const hello = "ce013625030ba8dba906f756967f9e9ca394464a"
	const scriptsTree, rootTree = "f5a951f69564218a58d208d3a0028d14762e2221", "42eb8fb0fda788e173116b62d34a88de7f7f5f90"
	for _, tc := range []struct {
		args     []string
		wantCode int
		want     string
	}{
		{[]string{"rev-parse", "--verify", "-q", "HEAD:./gate.sh"}, 0, hello + "\n"},
		{[]string{"rev-parse", "--verify", "-q", "HEAD:scripts/gate.sh"}, 0, hello + "\n"},
		{[]string{"rev-parse", "--verify", "-q", "HEAD:./edited.sh"}, 1, ""},
		{[]string{"rev-parse", "--verify", "-q", "v9.9.9:./gate.sh"}, 1, ""},
		{[]string{"rev-parse", "--verify", "-q", "HEAD:./"}, 0, scriptsTree + "\n"},
		{[]string{"rev-parse", "--verify", "-q", "HEAD:scripts"}, 0, scriptsTree + "\n"},
		{[]string{"rev-parse", "--verify", "-q", "HEAD:"}, 0, rootTree + "\n"},
		{[]string{"rev-parse", "--verify", "-q", "HEAD:../../x"}, 128, ""},
		{[]string{"hash-object", "--", "link.sh"}, 0, hello + "\n"},
		{[]string{"hash-object", "--", "gate.sh"}, 0, hello + "\n"},
		{[]string{"hash-object", "--", "edited.sh"}, 0, fakeGitHash("blob", []byte("new\n")) + "\n"},
		{[]string{"hash-object", "--", "absent.sh"}, 128, ""},
	} {
		result := fakeGitRun(t, git, dir, []string{}, tc.args...)
		if result.ExitCode != tc.wantCode || string(result.Stdout) != tc.want {
			t.Errorf(
				"git %q: exit %d stdout %q, want %d %q",
				tc.args,
				result.ExitCode,
				result.Stdout,
				tc.wantCode,
				tc.want,
			)
		}
	}
}
