package codexmeta

import (
	"testing"
	"time"
)

func TestDecodeSessionIndexLineReadsCodexRenameEntries(t *testing.T) {
	entry, err := DecodeSessionIndexLine(
		[]byte(
			`{"id":"01a09828-1306-74f3-9d73-81e3d3158ab9","thread_name":"PING_PROBE","updated_at":"2026-09-13T00:26:01.324772Z"}`,
		),
	)
	if err != nil {
		t.Fatalf("DecodeSessionIndexLine() error = %v", err)
	}
	if entry.ID != "01a09828-1306-74f3-9d73-81e3d3158ab9" || entry.ThreadName != "PING_PROBE" {
		t.Fatalf("entry = %#v", entry)
	}
	renamed, ok := entry.RenamedAt()
	want := time.Date(2026, 9, 13, 0, 26, 1, 324772000, time.UTC)
	if !ok || !renamed.Equal(want) {
		t.Fatalf("RenamedAt() = %v, %v; want %v, true", renamed, ok, want)
	}
}

// TestDecodeSessionIndexLineRefusesWhatNamesNoThread: a torn line (Codex
// appends while pfm reads) and an entry with no id are errors, never an entry.
func TestDecodeSessionIndexLineRefusesWhatNamesNoThread(t *testing.T) {
	for _, line := range []string{`{"id":"01a0","thread_na`, `{"thread_name":"orphan"}`, ``} {
		if entry, err := DecodeSessionIndexLine([]byte(line)); err == nil {
			t.Fatalf("DecodeSessionIndexLine(%q) = %#v, want an error", line, entry)
		}
	}
}

// TestSessionIndexEntryWithoutATimeHasNoRenameTime: an entry from before Codex
// 0.147 stamped times, or with an unreadable stamp, reports no time — never
// the zero instant a caller could compare as "long ago".
func TestSessionIndexEntryWithoutATimeHasNoRenameTime(t *testing.T) {
	for _, stamp := range []string{"", "yesterday"} {
		if renamed, ok := (SessionIndexEntry{ID: "a", UpdatedAt: stamp}).RenamedAt(); ok {
			t.Fatalf("RenamedAt() for %q = %v, true; want no time", stamp, renamed)
		}
	}
}
