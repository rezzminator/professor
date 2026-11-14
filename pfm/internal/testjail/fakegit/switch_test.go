package fakegit

import (
	"path/filepath"
	"testing"
)

// TestFakeGitSwitchesHead: checkout attaches a branch, detaches on a tag or
// with --detach, restores a path from the index, read from the directory it
// runs in, and refuses a pathspec the index lacks; merge --ff-only refuses a
// diverged target and says when it has nothing to do; reset --hard removes a
// staged new file.
func TestFakeGitSwitchesHead(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	first := commitFakeGitFiles(t, git, repo, "A", map[string]string{"a": "a\n"})
	git.Must(t, repo, "tag", "v1")
	git.Must(t, repo, "checkout", "-qb", "side", "v1")
	side := commitFakeGitFiles(t, git, repo, "side", map[string]string{"s": "s\n"})
	git.Must(t, repo, "checkout", "-q", "master")
	master := commitFakeGitFiles(t, git, repo, "B", map[string]string{"a": "b\n"})
	for _, tc := range []struct {
		args         []string
		head, symbol string
		tree         string
	}{
		{[]string{"checkout", "-q", "--detach", "v1"}, first, "", "a=a\n"},
		{[]string{"checkout", "-q", "side"}, side, "side", "a=a\n|s=s\n"},
		{[]string{"checkout", "-q", "v1"}, first, "", "a=a\n"},
		{[]string{"checkout", "-qB", "side", "master"}, master, "side", "a=b\n"},
	} {
		git.Must(t, repo, tc.args...)
		symbol := fakeGitRun(t, git, repo, []string{}, "symbolic-ref", "--short", "HEAD")
		if got := git.Must(
			t,
			repo,
			"rev-parse",
			"HEAD",
		); got != tc.head ||
			string(symbol.Stdout) != fakeGitLine(tc.symbol) ||
			fakeGitTree(t, repo) != tc.tree {
			t.Errorf("after %q: HEAD %s on %q, tree %q; want %s on %q, tree %q",
				tc.args, got, symbol.Stdout, fakeGitTree(t, repo), tc.head, tc.symbol, tc.tree)
		}
	}
	writeFakeGitFile(t, repo, "a", "local\n")
	git.Must(t, repo, "checkout", "--", "a")
	if got := fakeGitTree(t, repo); got != "a=b\n" {
		t.Fatalf("checkout -- a left %q, want a=b", got)
	}
	const unmatched = "error: pathspec 'nosuch' did not match any file(s) known to git\n"
	if result := fakeGitRun(t, git, repo, []string{}, "checkout", "--", "nosuch"); result.ExitCode != 1 ||
		string(result.Stderr) != unmatched {
		t.Errorf("checkout -- nosuch: exit %d stderr %q, want 1 %q", result.ExitCode, result.Stderr, unmatched)
	}
	if result := fakeGitRun(
		t,
		git,
		repo,
		[]string{},
		"merge",
		"--ff-only",
		"v1",
	); string(
		result.Stdout,
	) != "Already up to date.\n" {
		t.Fatalf("merge of an ancestor printed %q", result.Stdout)
	}
	git.Must(t, repo, "checkout", "-q", "--detach", side)
	if result := fakeGitRun(t, git, repo, []string{}, "merge", "--ff-only", master); result.ExitCode != 128 {
		t.Fatalf("merge --ff-only of a diverged target: exit %d, want 128", result.ExitCode)
	}
	commitFakeGitFiles(t, git, repo, "sub", map[string]string{"sub/f": "f\n"})
	writeFakeGitFile(t, repo, "sub/f", "local\n")
	git.Must(t, filepath.Join(repo, "sub"), "checkout", "--", "f")
	if got := fakeGitTree(t, repo); got != "a=a\n|s=s\n|sub/f=f\n" {
		t.Errorf("checkout -- f from sub/ left %q, want sub/f=f", got)
	}
	writeFakeGitFile(t, repo, "n", "n\n")
	git.Must(t, repo, "add", "n")
	git.Must(t, repo, "reset", "--hard")
	if tree, status := fakeGitTree(t, repo), fakeGitRun(t, git, repo, []string{}, "status", "--porcelain"); tree !=
		"a=a\n|s=s\n|sub/f=f\n" || len(status.Stdout) != 0 {
		t.Errorf("reset --hard over a staged new file: tree %q, status %q; want it gone and a clean status",
			tree, status.Stdout)
	}
}

// fakeGitLine is one printed line, or nothing for an empty value.
func fakeGitLine(value string) string {
	if value == "" {
		return ""
	}
	return value + "\n"
}
