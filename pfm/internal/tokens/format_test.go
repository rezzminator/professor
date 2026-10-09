package tokens

import (
	"math"
	"testing"
	"time"
)

func TestUTF16MeasuresCutsAndPadsAsJavaScript(t *testing.T) {
	if got := utf16Len("a😀"); got != 3 {
		t.Errorf("utf16Len(a😀) = %d, want 3: an astral rune is two code units", got)
	}
	strings := []struct{ name, got, want string }{
		{"slice keeps whole units", utf16Slice("a😀b", 0, 3), "a😀"},
		{"slice halving a pair writes U+FFFD", utf16Slice("a😀b", 0, 2), "a\ufffd"},
		{"slice past the end stops at it", utf16Slice("ab", 1, 9), "b"},
		{"slice with start past end is empty", utf16Slice("ab", 2, 1), ""},
		{"tail counts units", utf16Tail("abc😀", 2), "😀"},
		{"tail longer than the text is the text", utf16Tail("ab", 5), "ab"},
		{"padStart", padStart("x", 3), "  x"},
		{"padStart never truncates", padStart("xyz", 2), "xyz"},
		{"padEnd", padEnd("x", 3), "x  "},
		{"padEnd counts an emoji as two", padEnd("😀", 3), "😀 "},
		{"cut over width keeps width-1 and an ellipsis", cut("abcdef", 4), "abc…"},
		{"cut under width pads", cut("ab", 4), "ab  "},
		{"cut at width is unchanged", cut("abcd", 4), "abcd"},
	}
	for _, c := range strings {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestNumbersRenderAsJavaScript(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"fixed rounds", fixed(2.345678, 2), "2.35"},
		{"fixed tie rounds to even (the INTENDED delta)", fixed(1.25, 1), "1.2"},
		{"fixed negative zero is unsigned", fixed(math.Copysign(0, -1), 2), "0.00"},
		{"fixed tiny negative keeps its sign", fixed(-0.0001, 2), "-0.00"},
		{"fixed from 1e21 is num", fixed(1e21, 2), "1e+21"},
		{"fixed NaN", fixed(math.NaN(), 2), "NaN"},
		{"dollars from 100 are whole", dollars(123.6), "$124"},
		{"dollars below 100 carry cents", dollars(0.0044), "$0.00"},
		{"dollars rounding up to 100 keep cents", dollars(99.999), "$100.00"},
		{"pctText", pctText(1, 3), "33.3%"},
		{"pctText of nothing", pctText(1, 0), "0.0%"},
		{"pctText of NaN", pctText(1, math.NaN()), "0.0%"},
		{"millions", millions(1_500_000), "1.5M"},
		{"grouped thousands", grouped(1234567), "1,234,567"},
		{"grouped three decimals", grouped(1234567.891), "1,234,567.891"},
		{"grouped trims zeros", grouped(-1234.5), "-1,234.5"},
		{"grouped rounds to three decimals", grouped(0.12345), "0.123"},
		{"grouped below a thousand", grouped(999), "999"},
		{
			"isoTime",
			isoTime(float64(time.Date(2026, 9, 21, 6, 0, 0, 123e6, time.UTC).UnixMilli())),
			"2026-09-21T06:00:00.123Z",
		},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if got := fixedNum(0.12345, 3); got != 0.123 {
		t.Errorf("fixedNum(0.12345, 3) = %v, want 0.123", got)
	}
	rounds := []struct{ in, want float64 }{{2.5, 3}, {-2.5, -2}, {2.4, 2}, {-2.6, -3}, {0.5, 1}, {-1.5, -1}}
	for _, c := range rounds {
		if got := round(c.in); got != c.want {
			t.Errorf("round(%v) = %v, want %v: Math.round sends halves toward +Infinity", c.in, got, c.want)
		}
	}
	for _, in := range []float64{-0.4, -0.5, math.Copysign(0, -1)} {
		if got := round(in); got != 0 || !math.Signbit(got) {
			t.Errorf("round(%v) = %v, want -0", in, got)
		}
	}
	if got := round(math.Inf(1)); !math.IsInf(got, 1) {
		t.Errorf("round(+Inf) = %v, want +Inf", got)
	}
}

func TestModelNamesAndCacheMix(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"stem drops prefix and snapshot", modelStem("claude-sonnet-4-5-20250929"), "sonnet-4-5"},
		{"stem keeps an undated id", modelStem("claude-opus-5"), "opus-5"},
		{"stem of nothing", modelStem(""), "?"},
		{"codexShort drops gpt-", codexShort("gpt-5.6-sol"), "5.6-sol"},
		{"codexShort of nothing", codexShort(""), "?"},
		{"no writes", ttlMix(0, 0), "—"},
		{"5m only", ttlMix(10, 0), "5m"},
		{"1h only", ttlMix(0, 10), "1h"},
		{"both: the 1h share", ttlMix(1, 1), "1h 50%"},
		{"a sliver of 1h floors at 1%", ttlMix(1000, 1), "1h 1%"},
		{"nearly all 1h caps at 99%", ttlMix(1, 1000), "1h 99%"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestJavaScriptWhitespace(t *testing.T) {
	if got := collapseSpaces("a \v\u00a0\u2003 b\n\tc"); got != "a b c" {
		t.Errorf("collapseSpaces = %q, want %q: \\v and the Unicode spaces are JavaScript's \\s", got, "a b c")
	}
	if got := jsTrim("\u3000 x y \ufeff\u2028"); got != "x y" {
		t.Errorf("jsTrim = %q, want %q", got, "x y")
	}
	nonSpace := jsRegexp(`^\S+$`)
	if !nonSpace.MatchString("ab") || nonSpace.MatchString("a\u00a0b") {
		t.Errorf("jsRegexp \\S must match ab and refuse a no-break space")
	}
	if isJSSpace('x') || !isJSSpace('\u200a') || isJSSpace('\u200b') {
		t.Errorf("isJSSpace: U+200A is a space, U+200B and x are not")
	}
}

func TestDateParseReadsOffsetsAsGivenAndLocalTimesInTheZone(t *testing.T) {
	ms := func(text string) float64 {
		at, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			t.Fatalf("parse %s: %v", text, err)
		}
		return float64(at.UnixMilli())
	}
	cases := []struct {
		in   string
		want float64
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
		got, ok := dateParse(c.in, testZone)
		if !ok || got != c.want {
			t.Errorf("dateParse(%q) = %v, %v; want %v, true", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []string{"", "garbage", "2026-13-01", "2026-09-32", "2026-09-21T24:00:01Z", "2026-09-21T06:60Z"} {
		if got, ok := dateParse(bad, testZone); ok || !math.IsNaN(got) {
			t.Errorf("dateParse(%q) = %v, %v; want NaN, false", bad, got, ok)
		}
	}
}
