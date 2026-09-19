package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/installer"
)

// outsideAnyClone moves the test out of this checkout, so DiscoverSourceRepo
// finds nothing and the marker is the only source left to resolve.
func outsideAnyClone(t *testing.T) {
	t.Helper()
	t.Setenv("PFM_SOURCE_REPO", "")
	t.Chdir(t.TempDir())
}

func TestResolveInstallSourceRepoStaysSilentWhenNoMarkerWasEverRecorded(t *testing.T) {
	outsideAnyClone(t)
	var stderr bytes.Buffer
	if got := resolveInstallSourceRepo(t.TempDir(), &stderr); got != "" {
		t.Fatalf("resolved %q from a home with no marker", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("a first install must stay silent, got %q", stderr.String())
	}
}

func TestResolveInstallSourceRepoNamesARecordedCloneThatIsGone(t *testing.T) {
	outsideAnyClone(t)
	home := t.TempDir()
	gone := filepath.Join(t.TempDir(), "moved-away")
	marker := installer.SourceRepoPath(home)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(gone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if got := resolveInstallSourceRepo(home, &stderr); got != "" {
		t.Fatalf("resolved %q from a marker naming a vanished clone", got)
	}
	if !strings.Contains(stderr.String(), gone) || !strings.Contains(stderr.String(), "falling back") {
		t.Fatalf("a vanished recorded clone must be named before the fallback, got %q", stderr.String())
	}
}
