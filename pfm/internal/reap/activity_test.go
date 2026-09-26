package reap

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeActivityFixture is the one place every test in this file builds a
// scratch transcript, so every case starts from the same on-disk shape a
// real JSONL file has: newline-delimited records, no trailing structure.
func writeActivityFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The two shapes measured on the live fleet: a "timestamp" field carrying an
// ISO-8601 string (Claude and Codex both write this on nearly every record),
// and a "ts" field, which the fleet showed in both an ISO-8601 string and a
// bare epoch number — milliseconds once the number is too large to be
// seconds.
func TestLastRecordTimeParsesBothTimestampShapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want time.Time
	}{
		{
			name: "timestamp field, ISO-8601",
			line: `{"type":"assistant","timestamp":"2026-08-04T07:42:04.606Z"}`,
			want: time.Date(2026, 8, 4, 7, 42, 4, 606_000_000, time.UTC),
		},
		{
			name: "ts field, ISO-8601 string",
			line: `{"type":"history-suppression","ts":"2026-08-27T01:57:41.845Z"}`,
			want: time.Date(2026, 8, 27, 1, 57, 41, 845_000_000, time.UTC),
		},
		{
			name: "ts field, epoch milliseconds",
			line: `{"type":"event","ts":1798765432123}`,
			want: time.UnixMilli(1798765432123),
		},
		{
			name: "ts field, epoch seconds",
			line: `{"type":"event","ts":1798765432}`,
			want: time.Unix(1798765432, 0),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeActivityFixture(t, testCase.line)
			got, found := lastRecordTime(path)
			if !found {
				t.Fatalf("lastRecordTime(%q) found=false, want true", testCase.line)
			}
			if !got.Equal(testCase.want) {
				t.Fatalf("lastRecordTime(%q) = %v, want %v", testCase.line, got, testCase.want)
			}
		})
	}
}

// The trailing lines of a real transcript are mostly bookkeeping records
// with no timestamp of their own (custom-title, last-prompt, bridge-session,
// ...) — measured directly off the live fleet. The parser must walk past
// them to the last record that actually carries one.
func TestLastRecordTimeSkipsUntimestampedBookkeepingRecordsAtTheTail(t *testing.T) {
	path := writeActivityFixture(t,
		`{"type":"user","timestamp":"2026-08-04T07:42:05.269Z"}`,
		`{"type":"assistant","timestamp":"2026-08-04T07:42:09.688Z"}`,
		`{"type":"file-history-snapshot"}`,
		`{"type":"last-prompt"}`,
		`{"type":"custom-title","customTitle":"renamed, no timestamp"}`,
		`{"type":"bridge-session"}`,
	)
	got, found := lastRecordTime(path)
	if !found {
		t.Fatal("lastRecordTime found=false, want the last timestamped record")
	}
	want := time.Date(2026, 8, 4, 7, 42, 9, 688_000_000, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("lastRecordTime = %v, want %v (the metadata tail must not win)", got, want)
	}
}

// A chat mid-write can leave a truncated final line — a partial JSON object
// cut off by a process that died or is still streaming. That line must never
// abort the walk; the record before it is still readable.
func TestLastRecordTimeSkipsATruncatedTail(t *testing.T) {
	path := writeActivityFixture(t,
		`{"type":"assistant","timestamp":"2026-08-04T07:42:04.606Z"}`,
		`{"type":"assistant","timestamp":"2026-08-04T07:42:09.68`, // cut off mid-stream
	)
	got, found := lastRecordTime(path)
	if !found {
		t.Fatal("lastRecordTime found=false, want the last COMPLETE record")
	}
	want := time.Date(2026, 8, 4, 7, 42, 4, 606_000_000, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("lastRecordTime = %v, want %v", got, want)
	}
}

// A file that is entirely corrupt, or entirely bookkeeping with no
// timestamped record anywhere in it, is UNKNOWN — not "brand new", not
// "just written". Absence of a measurement is not a measurement of
// idleness.
func TestLastRecordTimeReportsUnknownWhenNothingParses(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "entirely corrupt", body: "{not json at all\n{{{\n"},
		{
			name: "valid JSON, no timestamp anywhere",
			body: `{"type":"custom-title"}` + "\n" + `{"type":"last-prompt"}` + "\n",
		},
		{name: "empty file", body: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(path, []byte(testCase.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, found := lastRecordTime(path); found {
				t.Fatalf("lastRecordTime(%q) found=true, want UNKNOWN", testCase.body)
			}
		})
	}
}

// A missing file — the crumb outlived the transcript it pointed to — is the
// same UNKNOWN, not a crash and not "idle forever".
func TestLastRecordTimeReportsUnknownForAMissingFile(t *testing.T) {
	if _, found := lastRecordTime(filepath.Join(t.TempDir(), "does-not-exist.jsonl")); found {
		t.Fatal("lastRecordTime found=true for a file that was never written")
	}
}

// socketActivity fails closed the moment ONE of a chat's several transcripts
// cannot be read — a /chat:branch split puts two on one socket, and an
// unreadable one means the socket's true idle time cannot be trusted even
// though its sibling parsed cleanly.
func TestSocketActivityFailsClosedWhenAnyPathIsUnreadable(t *testing.T) {
	readable := writeActivityFixture(t, `{"type":"user","timestamp":"2026-08-04T07:42:05.269Z"}`)
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	if _, found := socketActivity([]string{readable, missing}); found {
		t.Fatal("socketActivity found=true with one unreadable path in the set")
	}
}

func TestSocketActivityTakesTheNewestAcrossMultiplePaths(t *testing.T) {
	older := writeActivityFixture(t, `{"type":"user","timestamp":"2026-08-04T07:00:00.000Z"}`)
	newer := writeActivityFixture(t, `{"type":"user","timestamp":"2026-08-04T09:00:00.000Z"}`)
	got, found := socketActivity([]string{older, newer})
	if !found {
		t.Fatal("socketActivity found=false, want the newer of two readable paths")
	}
	want := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("socketActivity = %v, want %v", got, want)
	}
}
