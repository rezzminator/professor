package obs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/clock"
)

// TestRotatorCapsOneHomeAtKeepFiles pins the in-tree rotation: the current
// file never passes maxMB, the generations shift down, and the home never
// holds more than keep files.
func TestRotatorCapsOneHomeAtKeepFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "log", "pfm.jsonl")
	writer, err := newRotator(path, 3, 1, 0, clock.Real)
	if err != nil {
		t.Fatal(err)
	}
	// One megabyte is the cap; each record is 64 KiB, so 48 records fill three
	// generations and drop the oldest twice.
	record := append([]byte(strings.Repeat("r", 64*1024-1)), '\n')
	for written := 0; written < 48; written++ {
		if _, err := writer.Write(record); err != nil {
			t.Fatalf("write %d: %v", written, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("log directory holds %v, want 3 files (pfm.jsonl, .1, .2)", names)
	}
	for _, name := range []string{"pfm.jsonl", "pfm.jsonl.1", "pfm.jsonl.2"} {
		info, err := os.Stat(filepath.Join(filepath.Dir(path), name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Size() > 1024*1024 {
			t.Fatalf("%s = %d bytes, want at most 1 MiB", name, info.Size())
		}
	}
}

func TestRotatorAppendsToAnExistingFileAndDefaultsItsCaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	writer, err := newRotator(path, 0, 0, 0, clock.Real)
	if err != nil {
		t.Fatal(err)
	}
	if writer.keep != DefaultKeepFiles || writer.maxBytes != int64(DefaultMaxMB)*1024*1024 {
		t.Fatalf("zero settings gave keep=%d max=%d, want the package defaults", writer.keep, writer.maxBytes)
	}
	if _, err := writer.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newRotator(path, 0, 0, 0, clock.Real)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first\nsecond\n" {
		t.Fatalf("content = %q, want both records appended", string(content))
	}
}

func TestRotatorRefusesAnUnwritableDirectory(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newRotator(filepath.Join(blocked, "log", "pfm.jsonl"), 3, 1, 0, clock.Real); err == nil {
		t.Fatal("newRotator accepted a path under a regular file")
	}
}
