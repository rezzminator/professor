package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestOpenMemoryReadsNoEnvironmentAndWritesNoFile pins what OpenMemory never
// touches: a config that makes Open fail is never read, and the working
// directory and home stay empty.
func TestOpenMemoryReadsNoEnvironmentAndWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	home := filepath.Join(dir, "home")
	config := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(config, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvConfig, config)
	t.Setenv(paths.EnvStateDB, "")
	t.Setenv(paths.EnvCacheDB, "")
	if opened, err := Open(); err == nil {
		_ = opened.Close()
		t.Fatal("Open() with a broken config succeeded; the fixture does not break the environment")
	}
	memory, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory() = %v, want no environment read", err)
	}
	if err := memory.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "broken.json" {
		t.Fatalf("working directory %s holds %v (%v), want only broken.json", dir, entries, err)
	}
}
