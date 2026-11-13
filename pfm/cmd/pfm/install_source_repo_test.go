package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
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
	marker := paths.SourceRepoPath(home)
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
	if got, want := rooted(
		stderr.String(),
		gone,
	), "pfm install: inspect recorded source repository {root}: recorded source repository is unusable: stat {root}: no such file or directory; falling back to the release themes\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
