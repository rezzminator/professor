package fakegit

import "testing"

// TestFakeGitWritesRefuseAsGitDoes: a commit of an unchanged index, a tag or
// branch that exists and a pathspec that matches nothing each refuse.
func TestFakeGitWritesRefuseAsGitDoes(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	commitFakeGitFiles(t, git, repo, "A", map[string]string{"a": "a\n"})
	git.Must(t, repo, "tag", "v1")
	git.Must(t, repo, "branch", "side")
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"commit", "-qm", "again"}, 1},
		{[]string{"tag", "v1"}, 128},
		{[]string{"branch", "side"}, 128},
		{[]string{"add", "missing.txt"}, 128},
		{[]string{"init", "-q"}, 0},
	} {
		if result := fakeGitRun(t, git, repo, []string{}, tc.args...); result.ExitCode != tc.code {
			t.Errorf("git %q: exit %d, want %d (stderr %q)", tc.args, result.ExitCode, tc.code, result.Stderr)
		}
	}
	if got := git.Must(t, repo, "tag", "--list"); got != "v1" {
		t.Fatalf("a re-run init lost the history: tags %q, want v1", got)
	}
}
