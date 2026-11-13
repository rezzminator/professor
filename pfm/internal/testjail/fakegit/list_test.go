package fakegit

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFakeGitListsTheWorkTree: status --porcelain's two columns then the
// untracked files (each file with --untracked-files=all, an untracked
// directory once by default, none with =no), a pathspec narrowing them, a
// tracked file inside an ignored directory still read; ls-files' untracked
// then tracked files under the directory it runs in, relative to it;
// ls-tree's entries under a directory.
func TestFakeGitListsTheWorkTree(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	commitFakeGitFiles(t, git, repo, "A0", map[string]string{"out/kept.js": "kept\n"})
	commitFakeGitFiles(t, git, repo, "A", map[string]string{
		".gitignore":         "out/\n",
		"changed.txt":        "a\n",
		"gone.txt":           "a\n",
		"releases/v1.md":     "1\n",
		"releases/old/v0.md": "0\n",
	})
	writeFakeGitFile(t, repo, "changed.txt", "b\n")
	writeFakeGitFile(t, repo, "staged.txt", "new\n")
	git.Must(t, repo, "add", "staged.txt")
	writeFakeGitFile(t, repo, "untracked/note.md", "note\n")
	writeFakeGitFile(t, repo, "out/built.js", "built\n")
	writeFakeGitFile(t, repo, "out/kept.js", "changed\n")
	if err := os.Remove(filepath.Join(repo, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	const tracked = " M changed.txt\n D gone.txt\n M out/kept.js\nA  staged.txt\n"
	for _, tc := range []struct {
		dir  string
		args []string
		want string
	}{
		{"", []string{"status", "--porcelain", "--untracked-files=all"}, tracked + "?? untracked/note.md\n"},
		{"", []string{"status", "--porcelain"}, tracked + "?? untracked/\n"},
		{"", []string{"status", "--porcelain", "--untracked-files=no"}, tracked},
		{"", []string{"status", "--porcelain", "--", "changed.txt"}, " M changed.txt\n"},
		{"", []string{"status", "--porcelain", "changed.txt"}, " M changed.txt\n"},
		{
			"",
			[]string{"status", "--porcelain", "--untracked-files=all", "--ignored", "--", "."},
			tracked + "?? untracked/note.md\n!! out/built.js\n",
		},
		{"", []string{"status", "--porcelain", "--untracked-files=all", "--ignored", "--", "releases"}, ""},
		{
			"",
			[]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate"},
			"untracked/note.md\x00.gitignore\x00changed.txt\x00gone.txt\x00out/kept.js\x00releases/old/v0.md\x00" +
				"releases/v1.md\x00staged.txt\x00",
		},
		{
			"",
			[]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "releases/old"},
			"releases/old/v0.md\x00",
		},
		{
			"releases",
			[]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"},
			"old/v0.md\x00v1.md\x00",
		},
		{
			"releases",
			[]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "../changed.txt"},
			"../changed.txt\x00",
		},
		{"", []string{"ls-tree", "--name-only", "HEAD", "releases/"}, "releases/old\nreleases/v1.md\n"},
		{"", []string{"ls-tree", "--name-only", "HEAD", "missing/"}, ""},
	} {
		if result := fakeGitRun(t, git, filepath.Join(repo, tc.dir), []string{}, tc.args...); result.ExitCode != 0 ||
			string(result.Stdout) != tc.want {
			t.Errorf("git %q in %q: exit %d stdout %q, want 0 %q",
				tc.args, tc.dir, result.ExitCode, result.Stdout, tc.want)
		}
	}
}
