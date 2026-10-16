package themesync

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStateStoreRoundTripAndMissing(t *testing.T) {
	store := stateStore{dir: filepath.Join(t.TempDir(), "theme")}
	if _, found, err := store.load(); found || err != nil {
		t.Fatalf("load of a missing state = found %v, err %v; want neither", found, err)
	}
	want := themeState{
		Enabled: true, Palette: "solarized", Mode: ModeAuto, Appearance: Dark, Generation: 4,
		Claude:  map[string]string{"/h/.claude/themes/professor-gold.json": "backups/claude/professor-gold.json"},
		Herdr:   &herdrSnapshot{Span: "[theme]\nname = \"x\"\n", Added: "", Backup: "/h/c.toml.pfm-theme-1"},
		Ghostty: &ghosttySnapshot{Config: "/h/config", Added: "\n# m\nconfig-file = ?x\n", Backup: "/h/config.bak"},
	}
	if err := store.save(want); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.load()
	if err != nil || !found {
		t.Fatalf("load = found %v, err %v", found, err)
	}
	if got.Generation != 4 || got.Palette != "solarized" || got.Herdr == nil || got.Herdr.Span != want.Herdr.Span ||
		got.Ghostty == nil || got.Ghostty.Added != want.Ghostty.Added ||
		got.Claude["/h/.claude/themes/professor-gold.json"] != "backups/claude/professor-gold.json" {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	info, err := os.Stat(filepath.Join(store.dir, StateFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestStateStoreCorruptStateIsAnErrorNeverEmpty(t *testing.T) {
	store := stateStore{dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(store.dir, StateFileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.load(); err == nil {
		t.Fatal("a corrupt state file must be an error, not a fresh state")
	}
}

func TestStateStoreLockIsExclusiveUntilReleased(t *testing.T) {
	store := stateStore{dir: t.TempDir()}
	release, err := store.lock()
	if err != nil {
		t.Fatal(err)
	}
	probe, err := os.OpenFile(filepath.Join(store.dir, "pfm-theme.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("lock file missing while held: %v", err)
	}
	defer func() { _ = probe.Close() }()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("second flock while held = %v, want EWOULDBLOCK", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock after release = %v, want nil", err)
	}
}

func TestStateStoreBackupPathStaysInsideTheStateDir(t *testing.T) {
	store := stateStore{dir: "/state"}
	if got, want := store.backupPath(
		"claude",
		"professor-gold.json",
	), "/state/backups/claude/professor-gold.json"; got != want {
		t.Fatalf("backupPath = %q, want %q", got, want)
	}
}
