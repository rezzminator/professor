package fakegit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestFakeGitWorkFiles: the work tree git sees skips .git and nested
// repositories, and enters an ignored directory for the tracked files alone.
func TestFakeGitWorkFiles(t *testing.T) {
	repo := &fakeGitRepo{work: t.TempDir(), state: newFakeGitState(false)}
	repo.state.Index["out/kept.js"] = fakeGitEntry{Mode: "100644", Blob: fakeGitHash("blob", []byte("kept\n"))}
	for rel, body := range map[string]string{
		".gitignore": "out/\n", "out/kept.js": "changed\n", "out/new.js": "new\n", "out/deep/x.js": "x\n",
		"src/a.go": "a\n", "nested/x": "x\n",
	} {
		writeFakeGitFile(t, repo.work, rel, body)
	}
	if err := os.MkdirAll(filepath.Join(repo.work, "nested", fakeGitDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := repo.workFiles()
	if err != nil {
		t.Fatalf("workFiles in %s: %v", repo.work, err)
	}
	listed := make([]string, 0, len(files))
	for p := range files {
		listed = append(listed, p)
	}
	sort.Strings(listed)
	if got, want := strings.Join(listed, "|"), ".gitignore|out/kept.js|src/a.go"; got != want {
		t.Fatalf("workFiles in %s = %q, want %q", repo.work, got, want)
	}
}
