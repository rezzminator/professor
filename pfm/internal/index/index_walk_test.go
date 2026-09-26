package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWalkClaudeRootsUsesGivenSymlinkPaths(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "p")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "x.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, link := range []string{a, b} {
		if err := os.Symlink(filepath.Dir(shared), link); err != nil {
			t.Fatal(err)
		}
	}
	files, err := walkClaudeRoots(context.Background(), []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(a, "p", "x.jsonl"), filepath.Join(b, "p", "x.jsonl")}
	if len(files) != 2 || files[0].Path != want[0] || files[1].Path != want[1] {
		t.Fatalf("paths = %v, want %v", files, want)
	}
}
