package testjail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteExecutableNeverRunKeepsBodyAndExecMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude")
	if err := WriteExecutableNeverRun(path, []byte("production-candidate"), 0o700); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(body) != "production-candidate" {
		t.Fatalf("%s holds %q, want %q", path, body, "production-candidate")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("%s mode = %v, want %v", path, info.Mode().Perm(), os.FileMode(0o700))
	}
}
