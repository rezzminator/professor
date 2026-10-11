//go:build darwin

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwapPathsExchangesTwoEntriesAtomically(t *testing.T) {
	base := t.TempDir()
	first, second := filepath.Join(base, "first"), filepath.Join(base, "second")
	if err := os.WriteFile(first, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapPaths(first, second); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(first); err != nil || !info.IsDir() {
		t.Fatalf("first = %v, %v; want the directory", info, err)
	}
	if got, err := os.ReadFile(second); err != nil || string(got) != "one" {
		t.Fatalf("second = %q, %v; want the file", got, err)
	}
}
