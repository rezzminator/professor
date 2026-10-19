package obs

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
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
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	// The sidecar pfm.jsonl.lock is the cross-process rotation lock, not a generation.
	if len(entries) != 4 || !slices.Contains(names, "pfm.jsonl.lock") {
		t.Fatalf("log directory holds %v, want 3 files (pfm.jsonl, .1, .2) and the pfm.jsonl.lock sidecar", names)
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

func TestOpenRotatingLogKeepsDiagnosticAppendsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostic.jsonl")
	for run := range 3 {
		writer, err := OpenRotatingLog(path, 2, 1, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(strings.Repeat("r", 600*1024))); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{path, path + ".1"} {
		info, err := os.Stat(name)
		if err != nil || info.Size() != 600*1024 || info.Mode().Perm() != 0o600 {
			t.Fatalf("diagnostic generation %s: info=%v, err=%v", name, info, err)
		}
	}
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("diagnostic exceeded its two-file cap: %v", err)
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

// openPair opens two writers on one log, the way two pfm processes do.
func openPair(t *testing.T, keep, maxMB int, stderr io.Writer) (first, second *rotator, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	first, err := newRotator(path, keep, maxMB, 0, clock.Real, stderr)
	if err != nil {
		t.Fatal(err)
	}
	second, err = newRotator(path, keep, maxMB, 0, clock.Real, stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	return first, second, path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// TestRotatorReopensInsteadOfRotatingAFileAnotherWriterRotated: a writer
// whose file another process already renamed must attach to the fresh file at
// the path — never rotate it on its own stale byte count, which renamed a
// just-started tiny file into .1 and burned a generation slot on it.
func TestRotatorReopensInsteadOfRotatingAFileAnotherWriterRotated(t *testing.T) {
	first, _, path := openPair(t, 5, 1, io.Discard)
	full := strings.Repeat("f", 1024*1024-5) + "\n"
	if _, err := first.Write([]byte(full)); err != nil {
		t.Fatal(err)
	}
	// The second writer opened later than the first one's full record, so its
	// own count says the file is nearly full too.
	late, err := newRotator(path, 5, 1, 0, clock.Real, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := late.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := first.Write([]byte("tiny-a\n")); err != nil { // rotates: the full file becomes .1
		t.Fatal(err)
	}
	if _, err := late.Write([]byte("tiny-b\n")); err != nil { // stale count says full
		t.Fatal(err)
	}
	if got := readFile(t, path+".1"); got != full {
		t.Fatalf(
			".1 holds %d bytes, want the one full generation (%d): a stale writer rotated the fresh file",
			len(got),
			len(full),
		)
	}
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf(".2 exists (stat err %v): the fresh small file was rotated into a slot", err)
	}
	if got := readFile(t, path); got != "tiny-a\ntiny-b\n" {
		t.Fatalf("live file = %q, want both small records", got)
	}
}

// TestRotatorNeverWritesIntoARenamedFile: after another writer rotated, a
// writer still holding the old descriptor appends to the file at the path,
// not to the renamed generation.
func TestRotatorNeverWritesIntoARenamedFile(t *testing.T) {
	first, second, path := openPair(t, 5, 1, io.Discard)
	record := strings.Repeat("r", 600*1024) + "\n"
	for written := 0; written < 2; written++ { // the second one rotates
		if _, err := first.Write([]byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := second.Write([]byte("from-second\n")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path+".1"); got != record {
		t.Fatalf(
			".1 holds %d bytes, want exactly the rotated record (%d): a write landed in the renamed file",
			len(got),
			len(record),
		)
	}
	if got := readFile(t, path); got != record+"from-second\n" {
		t.Fatalf("live file = %d bytes, want the first writer's record and the second's line", len(got))
	}
}

// TestRotatorWritersSharingAHomeKeepWholeGenerations: several writers racing
// on one log rotate on the file's real size, so every kept generation is
// close to the cap and none passes it.
func TestRotatorWritersSharingAHomeKeepWholeGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	const writers, perWriter, recordSize = 4, 100, 20 * 1024
	record := []byte(strings.Repeat("r", recordSize-1) + "\n")
	var group sync.WaitGroup
	failures := make(chan error, writers)
	for range writers {
		writer, err := newRotator(path, 5, 1, 0, clock.Real, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		group.Add(1)
		go func() {
			defer group.Done()
			for range perWriter {
				if _, err := writer.Write(record); err != nil {
					failures <- err
					return
				}
			}
			if err := writer.Close(); err != nil {
				failures <- err
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	for _, generation := range []string{".1", ".2", ".3", ".4"} {
		info, err := os.Stat(path + generation)
		if err != nil {
			t.Fatalf("generation %s: %v", generation, err)
		}
		if info.Size() > 1024*1024 || info.Size() < 1024*1024-recordSize {
			t.Errorf(
				"generation %s = %d bytes, want a whole generation of at most 1 MiB and within one record of it",
				generation,
				info.Size(),
			)
		}
	}
	if info, err := os.Stat(path); err != nil || info.Size() > 1024*1024 {
		t.Errorf("live file stat %v err %v, want at most 1 MiB", info, err)
	}
}

// TestRotatorReportsARotationFailureAndRecovers: a rotation step that fails is
// reported on stderr and returned, and the next write after the cause is gone
// attaches to the log again.
func TestRotatorReportsARotationFailureAndRecovers(t *testing.T) {
	var stderr bytes.Buffer
	first, _, path := openPair(t, 2, 1, &stderr)
	// With keep 2 the oldest generation is .1; a non-empty directory there
	// cannot be dropped, so the rotation fails at its first step.
	blocker := filepath.Join(path+".1", "held")
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	record := []byte(strings.Repeat("r", 600*1024) + "\n")
	if _, err := first.Write(record); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write(record); err == nil {
		t.Fatal("a rotation that could not drop the oldest generation reported success")
	}
	if !strings.Contains(stderr.String(), path+".1") {
		t.Fatalf("the rotation failure did not reach stderr naming the generation: %q", stderr.String())
	}
	if err := os.RemoveAll(path + ".1"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("after\n")); err != nil {
		t.Fatalf("write after the cause was removed: %v", err)
	}
	if !strings.Contains(stderr.String(), "recovered") {
		t.Fatalf("the recovery was not announced: %q", stderr.String())
	}
	if !strings.HasSuffix(readFile(t, path), "after\n") {
		t.Fatal("the record after recovery is not in the live file")
	}
}
