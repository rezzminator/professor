package fakegit

import "testing"

// TestFakeGitIgnoreRules: the last matching .gitignore line decides; a
// trailing slash matches directories alone, a leading or inner slash anchors.
func TestFakeGitIgnoreRules(t *testing.T) {
	repo := &fakeGitRepo{work: t.TempDir()}
	writeFakeGitFile(t, repo.work, ".gitignore", "# build\nout/\n/engines/\n*.swp\n!keep.swp\ndocs/*.tmp\n")
	rules := repo.ignoreRules()
	for _, tc := range []struct {
		path string
		dir  bool
		want bool
	}{
		{"out", true, true},
		{"out", false, false},
		{"src/out", true, true},
		{"engines", true, true},
		{"src/engines", true, false},
		{"a.swp", false, true},
		{"deep/b.swp", false, true},
		{"keep.swp", false, false},
		{"docs/x.tmp", false, true},
		{"other/docs/x.tmp", false, false},
		{"README.md", false, false},
	} {
		if got := fakeGitIgnored(rules, tc.path, tc.dir); got != tc.want {
			t.Errorf("ignored(%q, dir=%v) = %v, want %v", tc.path, tc.dir, got, tc.want)
		}
	}
}
