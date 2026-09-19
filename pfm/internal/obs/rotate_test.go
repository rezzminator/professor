package obs

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/clock"
)

// TestRotatorCapsOneHomeAtKeepFiles pins the in-tree rotation: the current
// file never passes maxMB, the generations shift down, and the home never
// holds more than keep files.
func TestRotatorCapsOneHomeAtKeepFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "log", "pfm.jsonl")
	writer, err := newRotator(path, 3, 1, 0, clock.Real, io.Discard)
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
	writer, err := newRotator(path, 0, 0, 0, clock.Real, io.Discard)
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
	reopened, err := newRotator(path, 0, 0, 0, clock.Real, io.Discard)
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

// TestRotatorReportsAWriteFailureOnceThenRateLimitsThenAnnouncesRecovery is
// L2-F8: a write/rotate failure after open must not vanish into slog's
// discarded Handler.Handle error — it is surfaced on stderr once, rate
// limited after that, and a return to health is announced once too.
func TestRotatorReportsAWriteFailureOnceThenRateLimitsThenAnnouncesRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pfm.jsonl")
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	var stderr bytes.Buffer
	writer, err := newRotator(path, 0, 0, 0, fake, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	// Force every write to fail — the same shape a disk-full or an unlinked
	// file takes — by closing the underlying file out from under the rotator.
	if err := writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("one\n")); err == nil {
		t.Fatal("write on a closed file unexpectedly succeeded")
	}
	if lines := strings.Count(stderr.String(), "\n"); lines != 1 {
		t.Fatalf("first failure reported %d lines, want exactly 1: %s", lines, stderr.String())
	}
	if !strings.Contains(stderr.String(), path) {
		t.Fatalf("the failure report did not name the path: %s", stderr.String())
	}
	reportedOnce := stderr.String()

	if _, err := writer.Write([]byte("two\n")); err == nil {
		t.Fatal("write on a closed file unexpectedly succeeded")
	}
	if stderr.String() != reportedOnce {
		t.Fatalf("a second failure inside the rate-limit window reported again: %s", stderr.String())
	}

	fake.Advance(failureReportInterval)
	if _, err := writer.Write([]byte("three\n")); err == nil {
		t.Fatal("write on a closed file unexpectedly succeeded")
	}
	if lines := strings.Count(stderr.String(), "\n"); lines != 2 {
		t.Fatalf("a failure past the rate-limit window did not report again: %s", stderr.String())
	}

	// Recover: reopen the file (what a rotate, or a retried open, does) and
	// write again — the recovered state is announced exactly once.
	if err := writer.reopen(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("four\n")); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(stderr.String(), "\n"); lines != 3 {
		t.Fatalf("a recovered write did not announce once: %s", stderr.String())
	}
	if _, err := writer.Write([]byte("five\n")); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(stderr.String(), "\n"); lines != 3 {
		t.Fatalf("a second healthy write repeated the recovery announcement: %s", stderr.String())
	}
}

func TestRotatorRefusesAnUnwritableDirectory(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newRotator(filepath.Join(blocked, "log", "pfm.jsonl"), 3, 1, 0, clock.Real, io.Discard); err == nil {
		t.Fatal("newRotator accepted a path under a regular file")
	}
}
