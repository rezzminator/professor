package transcript

import (
	"testing"
	"time"
)

func TestParseTimestampReadsOffsetsAsGivenAndLocalTimesInTheZone(t *testing.T) {
	// UTC+3, a zone that is no developer host's (flight R8).
	testZone := time.FixedZone("UTC+3", 3*60*60)
	ms := func(text string) int64 {
		at, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			t.Fatalf("parse %s: %v", text, err)
		}
		return at.UnixMilli()
	}
	cases := []struct {
		in   string
		want int64
	}{
		{"2026-09-21T06:00:00Z", ms("2026-09-21T06:00:00Z")},
		{"2026-09-21T09:00:00", ms("2026-09-21T06:00:00Z")}, // no offset: the zone, UTC+3
		{"2026-09-21T09:00", ms("2026-09-21T06:00:00Z")},
		{"2026-09-21", ms("2026-09-21T00:00:00Z")}, // a bare date is UTC
		{"2026", ms("2026-01-01T00:00:00Z")},
		{"2026-09-21T06:00:00+02:00", ms("2026-09-21T04:00:00Z")},
		{"2026-09-21T06:00:00-0130", ms("2026-09-21T07:30:00Z")},
		{"2026-09-21 06:00:00.5Z", ms("2026-09-21T06:00:00.5Z")},
		{"2026-09-21T06:00:00.123456Z", ms("2026-09-21T06:00:00.123Z")},
		{"2026-09-21T24:00:00Z", ms("2026-09-22T00:00:00Z")},
		{"2026-02-30T00:00:00Z", ms("2026-03-02T00:00:00Z")}, // rolls over as V8 does
	}
	for _, c := range cases {
		got, ok := ParseTimestamp(c.in, testZone)
		if !ok || got.UnixMilli() != c.want {
			t.Errorf("ParseTimestamp(%q) = %v, %v; want %v, true", c.in, got, ok, c.want)
		}
	}
	if at, ok := ParseTimestamp("2026-09-21T06:00:00.0004567Z", testZone); !ok || at.Nanosecond() != 456_000 {
		t.Errorf("ParseTimestamp keeps microseconds as fromisoformat does: %v, %v; want 456µs", at, ok)
	}
	for _, bad := range []string{"", "garbage", "2026-13-01", "2026-09-32", "2026-09-21T24:00:01Z", "2026-09-21T06:60Z"} {
		if got, ok := ParseTimestamp(bad, testZone); ok || !got.IsZero() {
			t.Errorf("ParseTimestamp(%q) = %v, %v; want zero, false", bad, got, ok)
		}
	}
}
