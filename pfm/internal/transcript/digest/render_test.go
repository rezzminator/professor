package digest

import (
	"slices"
	"testing"
)

func TestRenderTextHelpersAreThePythonReaders(t *testing.T) {
	sizes := []struct {
		n    int
		want string
	}{{0, "0B"}, {999, "999B"}, {1000, "1.0K"}, {1250, "1.2K"}, {999_999, "1000.0K"}, {1_000_000, "1.0M"}, {2_550_000, "2.5M"}}
	for _, row := range sizes {
		if got := sizeText(row.n); got != row.want {
			t.Errorf("sizeText(%d) = %q, want %q", row.n, got, row.want)
		}
	}
	caps := []struct {
		text  string
		limit int
		want  string
	}{{"abcdef", 0, "abcdef"}, {"abcdef", 6, "abcdef"}, {"abcdef", 4, "abcd…[+2]"}, {"ééééé", 2, "éé…[+3]"}}
	for _, row := range caps {
		if got := capText(row.text, row.limit); got != row.want {
			t.Errorf("capText(%q, %d) = %q, want %q", row.text, row.limit, got, row.want)
		}
	}
	if got := clockText("2026-09-29T20:00:05.123Z"); got != "20:00:05" {
		t.Errorf("clockText of a UTC stamp = %q", got)
	}
	if got := clockText("2026-09-29T23:00:05+03:00"); got != "20:00:05" {
		t.Errorf("clockText of an offset stamp = %q, want its UTC time", got)
	}
	if got := clockText("not a time"); got != "--:--:--" {
		t.Errorf("clockText of no stamp = %q", got)
	}
	lines := lastTextLines("one\n\n  \ntwo  \nthree is long\n", 2, 5)
	if !slices.Equal(lines, []string{"two", "three…[+8]"}) {
		t.Errorf("lastTextLines = %q", lines)
	}
	if got := lastTextLines("one\ntwo", 0, 10); got != nil {
		t.Errorf("lastTextLines with count 0 = %q, want none", got)
	}
}
