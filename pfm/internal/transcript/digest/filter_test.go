package digest

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"
)

func TestFilterBoundsReadEachSpelling(t *testing.T) {
	lines := []struct {
		spec      string
		low, high float64
		usage     bool
	}{
		{spec: "7", low: 7, high: 7},
		{spec: "4-8", low: 4, high: 8},
		{spec: "11-", low: 11, high: math.Inf(1)},
		{spec: "9-3", usage: true},
		{spec: "a-b", usage: true},
	}
	for _, row := range lines {
		low, high, err := lineBounds(row.spec)
		var failure *Failure
		if row.usage != (errors.As(err, &failure) && failure.Usage) {
			t.Errorf("lineBounds(%q) err = %v, want usage %v", row.spec, err, row.usage)
			continue
		}
		if !row.usage && (low != row.low || high != row.high) {
			t.Errorf("lineBounds(%q) = %v..%v, want %v..%v", row.spec, low, high, row.low, row.high)
		}
	}
	for _, row := range []struct{ index, n, want int }{{2, 5, 2}, {9, 5, 5}, {-2, 5, 3}, {-9, 5, 0}} {
		if got := sliceBound(row.index, row.n); got != row.want {
			t.Errorf("sliceBound(%d, %d) = %d, want %d", row.index, row.n, got, row.want)
		}
	}
	first := time.Date(2026, 9, 29, 20, 0, 0, 0, time.UTC)
	last := first.Add(40 * time.Second)
	p := &parsed{t0: &first, t1: &last}
	times := []struct {
		spec string
		want time.Time
	}{
		{"+5s", first.Add(5 * time.Second)},
		{"-10s", last.Add(-10 * time.Second)},
		{"20:00:30", first.Add(30 * time.Second)},
		{"2026-09-29T23:00:20+03:00", first.Add(20 * time.Second)},
	}
	for _, row := range times {
		got, err := windowTime(row.spec, p)
		if err != nil || !got.Equal(row.want) {
			t.Errorf("windowTime(%q) = %v, %v; want %v", row.spec, got, err, row.want)
		}
	}
	for _, spec := range []string{"2026-02-30T10:00", "2026", "2026-09-29T24:00:00Z", "-3000000h"} {
		var failure *Failure
		if _, err := windowTime(spec, p); !errors.As(err, &failure) || !failure.Usage {
			t.Errorf("windowTime(%q) = %v, want a usage refusal (fromisoformat refuses it; a span past "+
				"Duration overflows)", spec, err)
		}
	}
	if _, err := windowTime("+5m", &parsed{}); err == nil {
		t.Error("a relative bound on a transcript without stamps is refused")
	}
}

// An event with no readable stamp sits where its neighbours put it, so a window
// keeps it with the events around it instead of dropping it uncounted.
func TestWindowPlacesAnUnstampedEventByItsNeighbours(t *testing.T) {
	early := &event{line: 1, ts: "2026-09-29T20:00:01Z", kind: kindPrompt}
	earlyNote := &event{line: 2, kind: kindNote}
	late := &event{line: 3, ts: "2026-09-29T20:00:09Z", kind: kindReply}
	lateNote := &event{line: 4, ts: "garbage", kind: kindNote}
	p := &parsed{events: []*event{early, earlyNote, late, lateNote}}
	got, err := selectEvents(p, &request{only: "prompt,reply,call,note,final", since: "2026-09-29T20:00:05Z"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []*event{late, lateNote}; !slices.Equal(got, want) {
		t.Errorf("--since 20:00:05 kept lines %v, want [3 4]", eventLines(got))
	}
	headless := &parsed{events: []*event{{line: 1, kind: kindNote}}}
	if got, _ := selectEvents(headless, &request{only: "note", since: "2026-09-29T20:00:05Z"}); len(got) != 1 {
		t.Errorf("an event no stamp can place was dropped by the window: kept %d, want 1", len(got))
	}
}

func eventLines(events []*event) []int {
	lines := make([]int, len(events))
	for at, ev := range events {
		lines[at] = ev.line
	}
	return lines
}
