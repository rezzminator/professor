package fakegit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFakeGitDiffAndLog: diff prints git's unified diff of one changed line
// with its context, a hunk per change more than six lines from the next, the
// nearest line above opening with an ASCII letter as the hunk's context, a
// mode-only header, and nothing for a staged file gone from the disk; log the
// commits after a base that touch a path.
func TestFakeGitDiffAndLog(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	base := commitFakeGitFiles(t, git, repo, "A", map[string]string{"t.md": "1\n2\n3\n4\n5\n6\n7\n8\n", "o.md": "o\n"})
	touched := commitFakeGitFiles(t, git, repo, "touches t", map[string]string{"t.md": "1\n2\n3\n4\nFIVE\n6\n7\n8\n"})
	commitFakeGitFiles(t, git, repo, "touches o", map[string]string{"o.md": "p\n"})
	last := commitFakeGitFiles(t, git, repo, "touches t again", map[string]string{"t.md": "0\n"})
	writeFakeGitFile(t, repo, "t.md", "1\n2\n3\n4\nfive\n6\n7\n8\n")
	oldBlob, newBlob := fakeGitHash(
		"blob",
		[]byte("1\n2\n3\n4\n5\n6\n7\n8\n"),
	), fakeGitHash(
		"blob",
		[]byte("1\n2\n3\n4\nfive\n6\n7\n8\n"),
	)
	wantDiff := "diff --git a/t.md b/t.md\nindex " + oldBlob[:7] + ".." + newBlob[:7] + " 100644\n--- a/t.md\n+++ b/t.md\n" +
		"@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n"
	diffArgs := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", base, "--", "t.md"}
	if result := fakeGitRun(t, git, repo, []string{}, diffArgs...); string(result.Stdout) != wantDiff {
		t.Errorf("diff = %q, want %q", result.Stdout, wantDiff)
	}
	log := git.Must(
		t,
		repo,
		"log",
		"--no-color",
		"--no-show-signature",
		"--reverse",
		"--format=%h %s",
		base+"..HEAD",
		"--",
		"t.md",
	)
	if want := touched[:7] + " touches t\n" + last[:7] + " touches t again"; log != want {
		t.Errorf("log = %q, want %q", log, want)
	}
	var seq strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&seq, "%d\n", i)
	}
	const funcs = "package x\nfunc alpha() {  \n\tl3\n\tl4\n\tl5\n\tl6\n\tl7\n\tl8\n\u00e9 sigma\n\tl10\n\tl11\n" +
		"\tl12\n\tl13\n\tl14\n\tl15\n\tl16\n\tl17\n\tl18\n\tl19\n\tl20\n\tl21\n"
	hunks := commitFakeGitFiles(t, git, repo, "hunks", map[string]string{
		"split.txt": seq.String(), "merged.txt": seq.String(), "f.go": funcs, "m.sh": "m\n",
	})
	writeFakeGitFile(t, repo, "split.txt", strings.Replace(strings.Replace(seq.String(), "\n2\n", "\ntwo\n", 1),
		"\n20\n", "\ntwenty\n", 1))
	writeFakeGitFile(t, repo, "merged.txt", strings.Replace(strings.Replace(seq.String(), "\n2\n", "\ntwo\n", 1),
		"\n9\n", "\nnine\n", 1))
	writeFakeGitFile(t, repo, "f.go",
		strings.Replace(strings.Replace(funcs, "\tl6\n", "\tL6\n", 1), "\tl18\n", "\tL18\n", 1))
	if err := os.Chmod(filepath.Join(repo, "m.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGitFile(t, repo, "q.txt", "q\n")
	git.Must(t, repo, "add", "q.txt")
	if err := os.Remove(filepath.Join(repo, "q.txt")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{
		{"split.txt", "diff --git a/split.txt b/split.txt\nindex e8823e1..1d17810 100644\n--- a/split.txt\n" +
			"+++ b/split.txt\n@@ -1,5 +1,5 @@\n 1\n-2\n+two\n 3\n 4\n 5\n" +
			"@@ -17,7 +17,7 @@\n 17\n 18\n 19\n-20\n+twenty\n 21\n 22\n 23\n"},
		{"merged.txt", "diff --git a/merged.txt b/merged.txt\nindex e8823e1..9145b6d 100644\n--- a/merged.txt\n" +
			"+++ b/merged.txt\n@@ -1,12 +1,12 @@\n 1\n-2\n+two\n 3\n 4\n 5\n 6\n 7\n 8\n-9\n+nine\n 10\n 11\n 12\n"},
		{"f.go", "diff --git a/f.go b/f.go\nindex cdb3955..a42e495 100644\n--- a/f.go\n+++ b/f.go\n" +
			"@@ -3,7 +3,7 @@ func alpha() {\n \tl3\n \tl4\n \tl5\n-\tl6\n+\tL6\n \tl7\n \tl8\n \u00e9 sigma\n" +
			"@@ -15,7 +15,7 @@ func alpha() {\n \tl15\n \tl16\n \tl17\n-\tl18\n+\tL18\n \tl19\n \tl20\n \tl21\n"},
		{"m.sh", "diff --git a/m.sh b/m.sh\nold mode 100644\nnew mode 100755\n"},
		{"q.txt", ""},
	} {
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", hunks, "--", tc.path}
		result := fakeGitRun(t, git, repo, []string{}, args...)
		if result.ExitCode != 0 || string(result.Stdout) != tc.want {
			t.Errorf("diff of %s: exit %d stdout %q, want 0 %q", tc.path, result.ExitCode, result.Stdout, tc.want)
		}
	}
}
