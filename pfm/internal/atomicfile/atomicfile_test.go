package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteReplacesWholeWithTheModeAndLeavesNoScratch pins the writer's
// contract: the new content lands whole with exactly the asked permission bits
// (not the process umask, not the old file's), a missing parent is created, and
// nothing but the target remains in the directory.
func TestWriteReplacesWholeWithTheModeAndLeavesNoScratch(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(directory, "config.json")
	if err := Write(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("second\n"), 0o600|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "second\n" {
		t.Fatalf("content = %q, %v; want the second write whole", content, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode() != 0o600 {
		t.Fatalf("mode = %v, %v; want exactly 0600 — only the permission bits", info.Mode(), err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("directory holds %v, %v; want the target alone", entries, err)
	}
}

// TestWriteFailureLeavesTheOldFileAndNoScratch pins the failure half: a write
// that cannot publish — here the target is a directory the rename cannot
// replace — reports which path failed, keeps what was there, and removes its
// scratch file.
func TestWriteFailureLeavesTheOldFileAndNoScratch(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "occupied")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Write(target, []byte("new"), 0o600)
	if err == nil || !strings.Contains(err.Error(), target) {
		t.Fatalf("Write over a directory = %v, want an error naming %s", err, target)
	}
	entries, readErr := os.ReadDir(directory)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "occupied" {
		t.Fatalf("directory holds %v, %v; want only the untouched target", entries, readErr)
	}
	if kept, _ := os.ReadFile(filepath.Join(target, "keep")); string(kept) != "kept" {
		t.Fatalf("the old content was disturbed: %q", kept)
	}
}

// TestWriteFailureReportsAScratchItCouldNotRemove pins that cleanup is not
// swallowed: when the publish fails and the scratch file cannot be removed
// either, the error names both — the caller learns a ".<name>.tmp-*" file was
// left behind instead of being told only that the rename failed.
func TestWriteFailureReportsAScratchItCouldNotRemove(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "occupied")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	denied := errors.New("unlink denied")
	restore := removeScratch
	removeScratch = func(string) error { return denied }
	t.Cleanup(func() { removeScratch = restore })

	err := Write(target, []byte("new"), 0o600)
	if err == nil || !errors.Is(err, denied) || !strings.Contains(err.Error(), ".occupied.tmp-") {
		t.Fatalf("Write = %v; want the replace failure joined with the unremoved scratch path", err)
	}
}

// TestWriteFromStreamsUnderTheLimitAndRefusesPastIt: a stream within the limit
// is published whole; one byte past it is ErrTooLarge, the old file stays and
// no scratch remains.
func TestWriteFromStreamsUnderTheLimitAndRefusesPastIt(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "body.bin")
	written, err := WriteFrom(path, strings.NewReader("0123456789"), 0o600, 10)
	if err != nil || written != 10 {
		t.Fatalf("WriteFrom at the limit = (%d, %v), want (10, nil)", written, err)
	}
	if _, err := WriteFrom(path, strings.NewReader("0123456789A"), 0o600, 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("WriteFrom one byte past the limit error = %v, want ErrTooLarge", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "0123456789" {
		t.Fatalf("the refused stream replaced the old file: %q, %v", got, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("scratch left behind after a refused stream: %v, %v", entries, err)
	}
}
