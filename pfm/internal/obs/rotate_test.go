package obs

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

const mebibyte = 1024 * 1024

// segmentStart is the fake clock's time in the rotator tests: a segment
// closed at it is named 2026-11-12T10-00-00.000Z.jsonl.
var segmentStart = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// openLogAt opens a rotator on a fresh log/pfm.jsonl with keep segments of
// one mebibyte, timed by timing, closed at cleanup.
func openLogAt(t *testing.T, path string, keep int, reduce bool, timing clock.Clock, stderr io.Writer) *rotator {
	t.Helper()
	writer, err := newRotator(path, keep, 1, reduce, timing, stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	return writer
}

// listDir names the files in dir, in order; an absent dir lists nothing.
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func mustWrite(t *testing.T, writer io.Writer, record string) {
	t.Helper()
	if _, err := writer.Write([]byte(record)); err != nil {
		t.Fatal(err)
	}
}

// TestRotatorRollsPastTheSegmentSize: the append that carries the segment past
// its size closes it into pfm.segments/ under the time it closed; one that
// reaches the size exactly, or stops a byte short, keeps it current.
func TestRotatorRollsPastTheSegmentSize(t *testing.T) {
	for _, tc := range []struct {
		size     int
		segments []string
		current  int
	}{
		{size: mebibyte - 1, segments: []string{}, current: mebibyte - 1},
		{size: mebibyte, segments: []string{}, current: mebibyte},
		{size: mebibyte + 1, segments: []string{"2026-11-12T10-00-00.000Z.jsonl"}, current: 0},
	} {
		path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
		writer := openLogAt(t, path, 10, true, clock.NewFake(segmentStart), io.Discard)
		record := strings.Repeat("r", tc.size-1) + "\n"
		mustWrite(t, writer, record)
		segments := listDir(t, filepath.Join(filepath.Dir(path), "pfm.segments"))
		if strings.Join(segments, ",") != strings.Join(tc.segments, ",") {
			t.Fatalf("size %d: pfm.segments = %v, want %v", tc.size, segments, tc.segments)
		}
		if got := len(readFile(t, path)); got != tc.current {
			t.Fatalf("size %d: current segment = %d bytes, want %d", tc.size, got, tc.current)
		}
		if len(tc.segments) == 1 {
			if got := readFile(t, filepath.Join(filepath.Dir(path), "pfm.segments", tc.segments[0])); got != record {
				t.Fatalf("size %d: the closed segment = %d bytes, want the record (%d)", tc.size, len(got), len(record))
			}
		}
	}
}

func TestOpenRotatingLogKeepsDiagnosticAppendsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostic.jsonl")
	writer, err := OpenRotatingLog(path, 2, 1, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	record := strings.Repeat("r", 600*1024-1) + "\n"
	for range 5 { // rolls after the 2nd and the 4th; the 1st segment falls behind 2 MiB
		mustWrite(t, writer, record)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if got := listDir(t, dir); strings.Join(got, ",") != "diagnostic.jsonl,diagnostic.jsonl.lock,diagnostic.segments" {
		t.Fatalf("diagnostic folder = %v, want the current segment, its lock and its segments, nothing kept", got)
	}
	segments := listDir(t, filepath.Join(dir, "diagnostic.segments"))
	if len(segments) != 1 || len(readFile(t, filepath.Join(dir, "diagnostic.segments", segments[0]))) != 2*len(record) {
		t.Fatalf("diagnostic.segments = %v, want the one newest segment of two records", segments)
	}
	if got := len(readFile(t, path)); got != len(record) {
		t.Fatalf("current segment = %d bytes, want one record", got)
	}
}

func TestRotatorRefusesAnUnwritableDirectory(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newRotator(
		filepath.Join(blocked, "log", "pfm.jsonl"),
		3,
		1,
		true,
		clock.Real,
		io.Discard,
	); err == nil {
		t.Fatal("newRotator accepted a path under a regular file")
	}
}

// TestRotatorAttachesToTheSegmentAnotherWriterOpened: a writer another process
// rolled past appends at most reattachAfter longer to the closed segment —
// whole and kept — and its next look, or the roll its stale offset asks for,
// attaches it to the current one.
func TestRotatorAttachesToTheSegmentAnotherWriterOpened(t *testing.T) {
	for _, tc := range []struct {
		idle   time.Duration
		closed string
	}{
		{idle: reattachAfter - time.Millisecond, closed: "full\nlate\n"},
		{idle: reattachAfter, closed: "full\n"},
	} {
		timing := clock.NewFake(segmentStart)
		path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
		first := openLogAt(t, path, 10, true, timing, io.Discard)
		second := openLogAt(t, path, 10, true, timing, io.Discard)
		full := strings.Repeat("f", mebibyte) + "\n"
		mustWrite(t, first, full) // rolls
		timing.Advance(tc.idle)
		mustWrite(t, second, "late\n")
		mustWrite(t, second, "next\n")
		closed := readFile(t, filepath.Join(filepath.Dir(path), "pfm.segments", "2026-11-12T10-00-00.000Z.jsonl"))
		if want := strings.Replace(tc.closed, "full\n", full, 1); closed != want {
			t.Fatalf(
				"idle %v: the closed segment = %q…, want %q…",
				tc.idle,
				closed[len(closed)-10:],
				want[len(want)-10:],
			)
		}
		wantCurrent := "late\nnext\n"
		if tc.closed != "full\n" {
			wantCurrent = "next\n"
		}
		if got := readFile(t, path); got != wantCurrent {
			t.Fatalf("idle %v: current segment = %q, want %q", tc.idle, got, wantCurrent)
		}
		if got := listDir(t, filepath.Join(filepath.Dir(path), "pfm.segments")); len(got) != 1 {
			t.Fatalf("idle %v: segments = %v, want only the one the first writer closed", tc.idle, got)
		}
	}
}

// TestRotatorWritersSharingAHomeKeepEveryRecordWhole: writers racing on one
// log lose no record and split none, and every closed segment passed the size.
func TestRotatorWritersSharingAHomeKeepEveryRecordWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	const writers, perWriter, recordSize = 4, 100, 20 * 1024
	var group sync.WaitGroup
	failures := make(chan error, writers)
	for index := range writers {
		writer, err := newRotator(path, 10, 1, true, clock.Real, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		record := []byte(strings.Repeat(string(rune('a'+index)), recordSize-1) + "\n")
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
	dir := filepath.Join(filepath.Dir(path), "pfm.segments")
	records := strings.Count(readFile(t, path), "\n")
	for _, name := range listDir(t, dir) {
		content := readFile(t, filepath.Join(dir, name))
		if len(content) <= mebibyte {
			t.Errorf("segment %s = %d bytes, want past the 1 MiB it rolled at", name, len(content))
		}
		for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
			if len(line) != recordSize-1 || strings.Count(line, line[:1]) != recordSize-1 {
				t.Fatalf("segment %s holds a split or mixed record of %d bytes", name, len(line))
			}
			records++
		}
	}
	if records != writers*perWriter {
		t.Fatalf("the log holds %d records, want %d", records, writers*perWriter)
	}
}

// TestRotatorReportsARollFailureAndRecovers: a roll that cannot close its
// segment keeps the current one live, so no record is lost; the failure is
// reported on stderr once inside the rate-limit window, and the first write
// after the cause is gone rolls and announces the recovery.
func TestRotatorReportsARollFailureAndRecovers(t *testing.T) {
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	first := openLogAt(t, path, 10, true, clock.NewFake(segmentStart), &stderr)
	blocker := filepath.Join(filepath.Dir(path), "pfm.segments")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := strings.Repeat("r", 600*1024) + "\n"
	for range 3 {
		mustWrite(t, first, record)
	}
	if got := len(readFile(t, path)); got != 3*len(record) {
		t.Fatalf("current segment = %d bytes, want all three records (%d)", got, 3*len(record))
	}
	failure := "pfm: activity log: create activity log segments " + blocker + ": mkdir " + blocker +
		": not a directory\n"
	if stderr.String() != failure {
		t.Fatalf("stderr = %q, want %q", stderr.String(), failure)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, first, "after\n")
	if want := failure + "pfm: activity log: writes to " + path + " recovered\n"; stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if got := readFile(t, path); got != "" {
		t.Fatalf("current segment after the recovered roll = %q, want empty", got)
	}
}

// TestRotatorReattachesPastAHandleThatIsAlreadyClosed: a handle closed under
// the writer fails the write that meets it, reported, and is dropped: the next
// write opens the path afresh.
func TestRotatorReattachesPastAHandleThatIsAlreadyClosed(t *testing.T) {
	var stderr bytes.Buffer
	timing := clock.NewFake(segmentStart)
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	writer := openLogAt(t, path, 10, true, timing, &stderr)
	if err := writer.file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("lost\n")); err == nil {
		t.Fatal("a write over a closed handle reported success")
	}
	mustWrite(t, writer, "kept\n")
	if got := readFile(t, path); got != "kept\n" {
		t.Fatalf("current segment = %q, want the record after the reattach", got)
	}
	want := "pfm: activity log: write activity log " + path + ": write " + path + ": file already closed\n" +
		"pfm: activity log: writes to " + path + " recovered\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

// BenchmarkRotatorWrite is one record's write through the activity log's
// writer: a record the size of the live log's average (165 bytes), the
// fleet's segment size, rolls included.
func BenchmarkRotatorWrite(b *testing.B) {
	writer, err := newRotator(filepath.Join(b.TempDir(), "log", "pfm.jsonl"), 10, 8, true, clock.Real, io.Discard)
	if err != nil {
		b.Fatal(err)
	}
	record := []byte(strings.Repeat("r", 164) + "\n")
	b.ResetTimer()
	for range b.N {
		if _, err := writer.Write(record); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := writer.Close(); err != nil {
		b.Fatal(err)
	}
}
