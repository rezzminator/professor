package ui

import (
	"strings"
	"testing"
)

func maskString(text string, mask []bool) string {
	var out strings.Builder
	for index, value := range []rune(text) {
		if index < len(mask) && mask[index] {
			out.WriteRune(value)
		} else {
			out.WriteRune('.')
		}
	}
	return out.String()
}

func TestMatchMaskLightsWhatTheFilterMatched(t *testing.T) {
	cases := []struct {
		name, text, query, want string
	}{
		{"contiguous run", "P:BUILDER", "build", "..BUILD.."},
		{
			"contiguous run mid-text",
			"schema migration plan",
			"mig",
			strings.Repeat(".", 7) + "mig" + strings.Repeat(".", 11),
		},
		{"subsequence letters in order", "alpha beta", "ab", "a.....b..."},
		{"case and accents fold", "Résumé notes", "RÉSU", "Résu........"},
		{"every token on its own", "two words here", "two here", "two......." + "here"},
		{"contiguous and subsequence tokens", "atlas builder", "atl bld", "atl...b..ld.."},
	}
	for _, test := range cases {
		if got := maskString(test.text, matchMask(test.text, test.query)); got != test.want {
			t.Errorf("%s: matchMask(%q, %q) lights %q, want %q", test.name, test.text, test.query, got, test.want)
		}
	}
}

func TestMatchMaskIsNilWhenNothingMatchesOrNothingIsAsked(t *testing.T) {
	for _, test := range []struct{ text, query string }{
		{"alpha", ""},
		{"alpha", "   "},
		{"alpha", "zzz"},
		{"", "a"},
		{"ab", "ba"},
	} {
		if got := matchMask(test.text, test.query); got != nil {
			t.Errorf("matchMask(%q, %q) = %v, want nil", test.text, test.query, got)
		}
	}
}

func TestHighlightSpansReassembleTheText(t *testing.T) {
	base := tone{fg: "#111111"}
	lit := tone{fg: "#eeeeee", bold: true}
	for _, test := range []struct{ text, query string }{
		{"P:BUILDER-2", "bld"},
		{"nothing to see", "zzz"},
		{"界面 needle 列对齐", "needle"},
		{"x", ""},
	} {
		spans := highlightSpans(test.text, test.query, base, lit)
		var joined strings.Builder
		for _, part := range spans {
			joined.WriteString(part.text)
		}
		if joined.String() != test.text {
			t.Errorf("%q/%q: spans reassemble to %q", test.text, test.query, joined.String())
		}
	}
	spans := highlightSpans("abcabc", "bc", base, lit)
	litCount := 0
	for _, part := range spans {
		if part.paint == lit {
			litCount++
		}
	}
	if litCount != 1 {
		t.Errorf("the first contiguous match alone is lit; got %d lit runs in %#v", litCount, spans)
	}
	plain := highlightSpans("abc", "", base, lit)
	if len(plain) != 1 || plain[0].paint != base {
		t.Errorf("no query is one run of the base tone, got %#v", plain)
	}
}

func TestRuneIndexHandlesEdges(t *testing.T) {
	if runeIndex([]rune("abc"), nil) != -1 || runeIndex([]rune("ab"), []rune("abc")) != -1 {
		t.Error("an empty or over-long pattern never matches")
	}
	if got := runeIndex([]rune("héllo"), []rune("llo")); got != 2 {
		t.Errorf("runeIndex counts runes, not bytes: got %d", got)
	}
}
