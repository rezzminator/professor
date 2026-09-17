package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/nudge"
)

func TestDoctorCrumbHealthAcceptsNudgeMetadataAndRejectsAnEmptyIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := nudge.RecordContext(dir, "session-a", 45); err != nil {
		t.Fatal(err)
	}
	if _, _, err := nudge.Decide(dir, "session-a", 45, 35, 10); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nudge-ctx-"), []byte("45\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, invalid, err := crumbHealth(dir)
	if err != nil || entries != 3 || invalid != 1 {
		t.Fatalf(
			"entries=%d invalid=%d err=%v; legitimate sample/band must pass, empty identity must fail",
			entries,
			invalid,
			err,
		)
	}
}
