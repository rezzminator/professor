package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSymlinkOverDirSwapsInTheLinkAndRemovesTheDirectory(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "skill")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SymlinkOverDir(target, source); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(target); err != nil || got != source {
		t.Fatalf("readlink = %q, %v; want %q", got, err, source)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 2 {
		t.Fatalf("scratch left behind: %v, %v", entries, err)
	}
}
