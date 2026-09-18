package obs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/clock"
)

// plantGeneration writes a rotated file and dates its last write age ago.
func plantGeneration(t *testing.T, path string, age time.Duration, now time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"msg":"old"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
	return false
}

// TestRetentionDeletesAgedGenerationsAtOpen pins log.keepDays on a fake clock:
// at open, a rotated file whose newest record is older than keepDays is gone, a
// younger one stays, and the live file is never deleted however old it is.
func TestRetentionDeletesAgedGenerationsAtOpen(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	timing := clock.NewFake(now)
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	plantGeneration(t, path, 40*24*time.Hour, now)
	plantGeneration(t, path+".1", 31*24*time.Hour, now)
	plantGeneration(t, path+".2", 29*24*time.Hour, now)
	plantGeneration(t, path+".3", 400*24*time.Hour, now)
	writer, err := newRotator(path, 5, 8, 30, timing)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if exists(t, path+".1") || exists(t, path+".3") {
		t.Fatal("a generation older than keepDays survived open")
	}
	if !exists(t, path+".2") {
		t.Fatal("a generation younger than keepDays was deleted")
	}
	if !exists(t, path) {
		t.Fatal("the live file was deleted — it is only ever rotated")
	}
}

// TestRetentionZeroKeepsEveryGeneration: keepDays 0 disables the time limit.
func TestRetentionZeroKeepsEveryGeneration(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	plantGeneration(t, path+".1", 900*24*time.Hour, now)
	writer, err := newRotator(path, 5, 8, 0, clock.NewFake(now))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if !exists(t, path+".1") {
		t.Fatal("keepDays 0 deleted a generation")
	}
}

// TestRetentionPrunesAtEveryRotation: a generation that ages past keepDays
// while the process runs is deleted at the next rotation, not at the next open.
func TestRetentionPrunesAtEveryRotation(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	timing := clock.NewFake(now)
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	writer, err := newRotator(path, 5, 1, 30, timing)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	// Planted after open, so open's prune never saw it; rename keeps mtime,
	// so it ages into .4 at the rotation and is dropped there.
	plantGeneration(t, path+".3", 31*24*time.Hour, now)
	record := append([]byte(strings.Repeat("r", 600*1024)), '\n')
	for written := 0; written < 2; written++ {
		if _, err := writer.Write(record); err != nil {
			t.Fatal(err)
		}
	}
	if !exists(t, path+".1") {
		t.Fatal("the second 600 KiB record did not rotate a 1 MiB file")
	}
	if exists(t, path+".4") || exists(t, path+".3") {
		t.Fatal("the aged generation survived the rotation")
	}
}

// TestRotatorRotatesAtMaxMB is the size limit alone: a record that fits is
// appended, the one that would carry the file past maxMB rotates first.
func TestRotatorRotatesAtMaxMB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	writer, err := newRotator(path, 5, 1, 30, clock.NewFake(time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	record := append([]byte(strings.Repeat("r", 600*1024)), '\n')
	if _, err := writer.Write(record); err != nil {
		t.Fatal(err)
	}
	if exists(t, path+".1") {
		t.Fatal("a record that fits rotated")
	}
	if _, err := writer.Write(record); err != nil {
		t.Fatal(err)
	}
	if !exists(t, path+".1") {
		t.Fatal("a record past maxMB did not rotate")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(record)) {
		t.Fatalf("live file holds %d bytes, want exactly the record that rotated in", info.Size())
	}
}
