package digest

import (
	"errors"
	"math"
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
