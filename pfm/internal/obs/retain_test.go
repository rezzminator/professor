package obs

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// plantSegment writes a closed segment of content into the log at path's
// pfm.segments/, named name.
func plantSegment(t *testing.T, path, name, content string) string {
	t.Helper()
	dir := segmentsDir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(dir, name)
	if err := os.WriteFile(segment, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return segment
}

// TestRetentionKeepsTheNewestKeepBytesWhole: with two segments of one
// mebibyte kept, a segment that starts within the newest 2 MiB stays whole,
// though it carries the sum past them (a roll closes a segment just past its
// size); one starting at 2 MiB or older is reduced.
func TestRetentionKeepsTheNewestKeepBytesWhole(t *testing.T) {
	for _, tc := range []struct {
		newest int
		kept   []string
	}{
		{newest: mebibyte, kept: []string{"b.jsonl", "c.jsonl"}},
		{newest: mebibyte + 1, kept: []string{"b.jsonl", "c.jsonl"}},
		{newest: 2 * mebibyte, kept: []string{"c.jsonl"}},
	} {
		path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
		writer := openLogAt(t, path, 2, true, clock.NewFake(segmentStart), io.Discard)
		info := `{"ts":"2026-11-12T09:00:00Z","level":"INFO","msg":"x"}` + "\n"
		plantSegment(t, path, "a.jsonl", info)
		plantSegment(t, path, "b.jsonl", strings.Repeat("b", mebibyte-1)+"\n")
		plantSegment(t, path, "c.jsonl", strings.Repeat("c", tc.newest-1)+"\n")
		if err := writer.retain(); err != nil {
			t.Fatal(err)
		}
		if got := listDir(t, segmentsDir(path)); strings.Join(got, ",") != strings.Join(tc.kept, ",") {
			t.Fatalf("newest %d: segments = %v, want %v", tc.newest, got, tc.kept)
		}
	}
}

// TestRetentionKeepsWarningsAndErrorsByteForByte: a reduced segment's WARN
// and ERROR records — and a damaged line, which may be an error's — land in
// pfm.kept/{UTC day of the record}.jsonl exactly as written, nothing else;
// a last line missing its newline gets one, and the segment is gone.
func TestRetentionKeepsWarningsAndErrorsByteForByte(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	writer := openLogAt(t, path, 1, true, clock.NewFake(segmentStart), io.Discard)
	lines := []string{
		`{"ts":"2026-11-11T23:59:59.5Z","level":"DEBUG","msg":"d"}`,
		`{"ts":"2026-11-11T23:59:59.6Z","level":"WARN","msg":"w1"}`,
		`{"ts":"2026-11-12T00:00:00.1+01:00","level":"ERROR","msg":"e-local"}`,
		`{"ts":"2026-11-12T00:00:01Z","level":"INFO","msg":"i"}`,
		`{"level":"ERROR+2","ts":"2026-11-12T00:00:02Z","msg":"e2"}`,
		`not a record`,
		`{"ts":"2026-11-12T00:00:03Z","level":"WARN","msg":"last"}`,
	}
	plantSegment(t, path, "2026-11-12T00-00-04.000Z.jsonl", strings.Join(lines, "\n"))
	plantSegment(t, path, "z.jsonl", strings.Repeat("z", mebibyte-1)+"\n")
	if err := writer.retain(); err != nil {
		t.Fatal(err)
	}
	kept := keptDir(path)
	if got := listDir(t, kept); strings.Join(got, ",") != "2026-11-11.jsonl,2026-11-12.jsonl" {
		t.Fatalf("pfm.kept = %v, want one file per day", got)
	}
	// e-local is 23:00:00.1 UTC on the 11th.
	if got, want := readFile(t, filepath.Join(kept, "2026-11-11.jsonl")), lines[1]+"\n"+lines[2]+"\n"; got != want {
		t.Fatalf("%s/2026-11-11.jsonl = %q, want %q", kept, got, want)
	}
	want := lines[4] + "\n" + lines[5] + "\n" + lines[6] + "\n"
	if got := readFile(t, filepath.Join(kept, "2026-11-12.jsonl")); got != want {
		t.Fatalf("%s/2026-11-12.jsonl = %q, want %q", kept, got, want)
	}
	if got := listDir(t, segmentsDir(path)); strings.Join(got, ",") != "z.jsonl" {
		t.Fatalf("segments = %v, want only the newest", got)
	}
}

// TestRetentionFailureKeepsTheSegment: a reduction that cannot keep its
// warnings leaves the segment where it was and names why.
func TestRetentionFailureKeepsTheSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "pfm.jsonl")
	writer := openLogAt(t, path, 1, true, clock.NewFake(segmentStart), io.Discard)
	old := plantSegment(t, path, "a.jsonl", `{"ts":"2026-11-12T09:00:00Z","level":"WARN","msg":"w"}`+"\n")
	plantSegment(t, path, "b.jsonl", strings.Repeat("b", mebibyte-1)+"\n")
	if err := os.WriteFile(keptDir(path), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writer.retain()
	want := "create kept activity log " + keptDir(path) + ": mkdir " + keptDir(path) + ": not a directory"
	if err == nil || err.Error() != want {
		t.Fatalf("retain err = %v, want %q", err, want)
	}
	if _, statErr := os.Stat(old); statErr != nil {
		t.Fatalf("the unreduced segment is gone: %v", statErr)
	}
}
