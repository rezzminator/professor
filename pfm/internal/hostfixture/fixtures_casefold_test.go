package hostfixture

import (
	"os"
	"testing"
)

// TestCaseFoldProbeReportsWhatTheFilesystemActuallyDid cross-checks Folds
// against the directory's real entry count — the assertion holds
// regardless of which way the underlying filesystem actually behaves.
func TestCaseFoldProbeReportsWhatTheFilesystemActuallyDid(t *testing.T) {
	fixture := CaseFoldProbe(t)

	entries, err := os.ReadDir(fixture.Dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", fixture.Dir, err)
	}
	wantEntries := 2
	if fixture.Folds {
		wantEntries = 1
	}
	if len(entries) != wantEntries {
		t.Fatalf("Folds=%v but %s holds %d entr(ies), want %d", fixture.Folds, fixture.Dir, len(entries), wantEntries)
	}
}
