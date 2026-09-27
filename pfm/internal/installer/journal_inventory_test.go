package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallJournalsInventoriesNamedDirectories(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "state", "pfm", "migrations")
	for id, body := range map[string]string{
		"20260101T000000Z": `[{"result":"applied"}]`,
		"20260102T000000Z": `[{"result":"pending"}]`,
		"20260103T000000Z": `[{"result":"pending"}]`,
		"20260105T000000Z": `{`,
	} {
		layoutWrite(t, filepath.Join(root, id, "journal.json"), body)
	}
	for _, id := range []string{"20260104T000000Z", "notes"} {
		if err := os.MkdirAll(filepath.Join(root, id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	layoutWrite(t, filepath.Join(root, "20260103T000000Z", layoutRolledBackMarker), "done")
	got, err := InstallJournals(home)
	if err != nil || len(got) != 5 {
		t.Fatalf("journals=%+v err=%v", got, err)
	}
	for index, want := range []struct {
		id         string
		pending    bool
		rolledBack bool
		broken     bool
	}{
		{"20260101T000000Z", false, false, false},
		{"20260102T000000Z", true, false, false},
		{"20260103T000000Z", true, true, false},
		{"20260104T000000Z", false, false, false},
		{"20260105T000000Z", false, false, true},
	} {
		item := got[index]
		if item.ID != want.id || item.Dir != filepath.Join(root, want.id) || item.Pending != want.pending ||
			item.RolledBack != want.rolledBack || (item.Err != nil) != want.broken || item.Bytes == 0 && index != 3 {
			t.Errorf("journal %d=%+v, want %+v", index, item, want)
		}
	}
}

func TestInstallJournalsAbsentAndUnreadableRoot(t *testing.T) {
	home := t.TempDir()
	got, err := InstallJournals(home)
	if err != nil || len(got) != 0 {
		t.Fatalf("absent root=%+v err=%v", got, err)
	}
	root := filepath.Join(home, ".local", "state", "pfm", "migrations")
	layoutWrite(t, root, "not a directory")
	if _, err := InstallJournals(home); err == nil {
		t.Fatal("listing a file as a directory succeeded")
	}
}

func TestInstallJournalsTreatsRestoredAsClosed(t *testing.T) {
	home := t.TempDir()
	layoutWrite(t, filepath.Join(home, ".local", "state", "pfm", "migrations", "20260101T000000Z", "journal.json"),
		`[{"result":"applied"},{"result":"restored"}]`)
	got, err := InstallJournals(home)
	if err != nil || len(got) != 1 || got[0].Pending || got[0].Err != nil {
		t.Fatalf("restored journal=%+v err=%v", got, err)
	}
}
