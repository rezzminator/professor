package reap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
)

// TestAvailableKBReadsMemAvailable is the control: a well-formed meminfo
// reads back the value on its MemAvailable line.
func TestAvailableKBReadsMemAvailable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "meminfo"),
		[]byte("MemTotal:       16384000 kB\nMemAvailable:    8192000 kB\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{paths: paths.Values{ProcRoot: root}}
	got, err := runner.availableKB()
	if err != nil || got != 8192000 {
		t.Fatalf("availableKB() = %d, %v, want 8192000, nil", got, err)
	}
}

// TestAvailableKBAbsentMeminfoIsNotAnError pins the legitimate absence: no
// /proc on this platform (or a jail fixture that never staged meminfo) reads
// back a plain (0, nil) — never an error, and never confused with a genuine
// read/parse failure.
func TestAvailableKBAbsentMeminfoIsNotAnError(t *testing.T) {
	runner := &Runner{paths: paths.Values{ProcRoot: t.TempDir()}}
	got, err := runner.availableKB()
	if err != nil || got != 0 {
		t.Fatalf("availableKB() = %d, %v, want 0, nil for an absent meminfo", got, err)
	}
}

// TestAvailableKBUnreadableMeminfoIsAnError pins the regression: a meminfo
// that EXISTS but cannot be read (here: a directory sits where the file
// belongs, which always fails os.ReadFile with EISDIR regardless of the
// test's own uid/gid) used to be silently folded into the same 0 a genuine
// absence returns — a probe that could not run rendering as "0 KB
// available", the one number that would itself be alarming.
func TestAvailableKBUnreadableMeminfoIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "meminfo"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{paths: paths.Values{ProcRoot: root}}
	got, err := runner.availableKB()
	if err == nil || got != 0 {
		t.Fatalf("availableKB() = %d, %v, want an error for an unreadable meminfo", got, err)
	}
}

// TestAvailableKBMissingMemAvailableLineIsAnError pins the other silent
// path the old code folded into 0: a meminfo that exists and reads fine but
// carries no MemAvailable line at all — a format this reader does not
// understand, not a legitimate zero.
func TestAvailableKBMissingMemAvailableLineIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "meminfo"), []byte("MemTotal: 16384000 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{paths: paths.Values{ProcRoot: root}}
	got, err := runner.availableKB()
	if err == nil || got != 0 || !strings.Contains(err.Error(), "MemAvailable") {
		t.Fatalf("availableKB() = %d, %v, want an error naming the missing MemAvailable line", got, err)
	}
}
