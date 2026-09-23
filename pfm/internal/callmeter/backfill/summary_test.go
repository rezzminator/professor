package backfill

import (
	"strings"
	"testing"
)

// TestSummaryStringNamesEveryFault pins Summary.String's shape directly: the
// count line plus one line per config dir without projects/, per unreadable
// transcript and per malformed line — the same text the backfill tests above
// check by running FromTranscripts end to end.
func TestSummaryStringNamesEveryFault(t *testing.T) {
	s := Summary{
		ConfigDirs:      []string{"/a", "/b"},
		NoProjects:      []string{"/b"},
		TranscriptsRead: 3,
		Unreadable:      []Problem{{Path: "/a/broken.jsonl", Err: "is a directory"}},
		Malformed:       []Problem{{Path: "/a/bad.jsonl", Line: 1, Err: "invalid character"}},
		EntriesSkipped:  2,
		CallsInserted:   9,
		CallsFilled:     1,
		Requests:        4,
		Agents:          2,
		Retired:         1,
	}
	text := s.String()
	for _, want := range []string{
		"3 transcripts read in 2 config dirs, 1 unreadable, 1 malformed lines",
		"calls: 9 inserted, 1 filled; requests: 4; agents: 2; entries older than the window: 2; retired 1",
		"no projects/ in config dir /b",
		"unreadable: /a/broken.jsonl: is a directory",
		"malformed: /a/bad.jsonl line 1: invalid character",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Summary.String() missing %q, got:\n%s", want, text)
		}
	}
}
