package tokens

import "testing"

func TestBriefItemsAndPaths(t *testing.T) {
	if got := briefItems("1. first\n  2) second\n- dash\n* star\n## heading\nplain\n-nospace"); got != 5 {
		t.Errorf("briefItems = %v, want 5", got)
	}
	if got := briefPaths("read pfm/a.go, then pfm/a.go again and docs/x.md; not a.go or pfm/"); got != 2 {
		t.Errorf("briefPaths = %v, want 2 distinct paths", got)
	}
}

func TestRegexpSourceIsTheJavaScriptSource(t *testing.T) {
	cases := map[string]string{
		"":         "(?:)",
		"a/b":      `a\/b`,
		`a\/b`:     `a\/b`,
		"[/]x":     "[/]x",
		"a\nb\rc":  `a\nb\rc`,
		"flights-": "flights-",
	}
	for pattern, want := range cases {
		if got := regexpSource(pattern); got != want {
			t.Errorf("regexpSource(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestMedianAndSumOverBriefRows(t *testing.T) {
	rows := []*briefRow{{calls: 9}, {calls: 1}, {calls: 4}}
	calls := func(row *briefRow) float64 { return row.calls }
	if got := medianOf(rows, calls); got != 4 {
		t.Errorf("medianOf = %v, want 4", got)
	}
	if got := sumOf(rows, calls); got != 14 {
		t.Errorf("sumOf = %v, want 14", got)
	}
	if medianOf(nil, calls) != 0 || sumOf(nil, calls) != 0 {
		t.Error("no rows: want 0 and 0")
	}
}
