package fakegit

import "testing"

// TestFakeGitHashesObjectsAsGitDoes: blob and tree ids are git's own, and a
// revision resolves by tag, branch, id prefix and ^{commit} peel.
func TestFakeGitHashesObjectsAsGitDoes(t *testing.T) {
	hello := fakeGitHash("blob", []byte("hello\n"))
	for _, tc := range []struct{ name, got, want string }{
		{"blob hello", hello, "ce013625030ba8dba906f756967f9e9ca394464a"},
		{"empty tree", fakeGitTreeID(map[string]fakeGitEntry{}, ""), "4b825dc642cb6eb9a060e54bf8d69288fbee4904"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s id = %s, want %s", tc.name, tc.got, tc.want)
		}
	}
	git, repo := newFakeGitRepo(t)
	first := commitFakeGitFiles(t, git, repo, "A", map[string]string{"a": "a\n"})
	git.Must(t, repo, "tag", "-a", "-m", "release", "v1.0.0")
	git.Must(t, repo, "branch", "kept")
	tag := git.Must(t, repo, "rev-parse", "--verify", "v1.0.0")
	commitFakeGitFiles(t, git, repo, "B", map[string]string{"a": "b\n"})
	for rev, want := range map[string]string{
		"v1.0.0^{commit}": first, "kept": first, first[:9]: first, "refs/tags/v1.0.0": tag,
	} {
		if got := git.Must(t, repo, "rev-parse", "--verify", rev); got != want {
			t.Errorf("rev-parse --verify %s = %s, want %s", rev, got, want)
		}
	}
	if tag == first {
		t.Fatalf("annotated tag id %s equals its commit's: the tag object was never written", tag)
	}
}
