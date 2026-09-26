package hostfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestOddPathsDirCarriesASpaceAndNonASCII(t *testing.T) {
	fixture := OddPaths(t)
	base := filepath.Base(fixture.Dir)
	if !strings.Contains(base, " ") {
		t.Fatalf("OddPaths dir %q has no space", fixture.Dir)
	}
	if isASCII(base) {
		t.Fatalf("OddPaths dir %q is pure ASCII, want a non-ASCII rune", fixture.Dir)
	}
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// TestOddPathsHomeIsAReallyUsableFilesystemPath proves the odd home is not
// just a string: paths.Resolve() (reading the real jailed environment
// OddPaths set) agrees with it, and a file actually round-trips through it.
func TestOddPathsHomeIsAReallyUsableFilesystemPath(t *testing.T) {
	fixture := OddPaths(t)

	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatalf("paths.Resolve() after OddPaths: %v", err)
	}
	if resolved.Home != fixture.Values.Home {
		t.Fatalf("paths.Resolve().Home = %q, want %q", resolved.Home, fixture.Values.Home)
	}

	target := filepath.Join(fixture.Values.Home, "probe.txt")
	if err := os.WriteFile(target, []byte("odd-path-ok"), 0o600); err != nil {
		t.Fatalf("write under the odd home: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back under the odd home: %v", err)
	}
	if string(content) != "odd-path-ok" {
		t.Fatalf("content = %q, want %q", content, "odd-path-ok")
	}
}
