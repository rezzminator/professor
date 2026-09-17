package hostfixture

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSymlinkedConfigDirResolvesThroughToThePhysicalDirectory proves the
// fixture's whole claim: ConfigDir is a symlink (Lstat's mode carries
// ModeSymlink), and a normal Stat through it lands on Physical, which is a
// real directory that actually exists and holds content written under it.
func TestSymlinkedConfigDirResolvesThroughToThePhysicalDirectory(t *testing.T) {
	fixture := SymlinkedConfigDir(t)

	linkInfo, err := os.Lstat(fixture.ConfigDir)
	if err != nil {
		t.Fatalf("Lstat(ConfigDir): %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("ConfigDir %q is not a symlink (mode=%v)", fixture.ConfigDir, linkInfo.Mode())
	}

	resolved, err := filepath.EvalSymlinks(fixture.ConfigDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(ConfigDir): %v", err)
	}
	physicalResolved, err := filepath.EvalSymlinks(fixture.Physical)
	if err != nil {
		t.Fatalf("EvalSymlinks(Physical): %v", err)
	}
	if resolved != physicalResolved {
		t.Fatalf("ConfigDir resolves to %q, want Physical %q", resolved, physicalResolved)
	}

	marker := filepath.Join(fixture.ConfigDir, "marker.txt")
	if err := os.WriteFile(marker, []byte("through-the-link"), 0o600); err != nil {
		t.Fatalf("write through ConfigDir: %v", err)
	}
	direct := filepath.Join(fixture.Physical, "marker.txt")
	content, err := os.ReadFile(direct)
	if err != nil {
		t.Fatalf("read the physical file directly: %v", err)
	}
	if string(content) != "through-the-link" {
		t.Fatalf("content read from Physical = %q, want %q", content, "through-the-link")
	}
}
