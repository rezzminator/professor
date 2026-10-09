package tokens

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/rezzminator/professor/pfm/internal/jsvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// The report's text measures and cuts strings in UTF-16 code units, as the
// Node engine did: a title holding an emoji pads and truncates identically.

func utf16Len(text string) int {
	n := 0
	for _, r := range text {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// utf16Slice is String.prototype.slice(start, end) with both bounds already
// non-negative; a surrogate pair cut in half renders as U+FFFD, as Node writes it.
func utf16Slice(text string, start, end int) string {
	units := utf16.Encode([]rune(text))
	end = min(end, len(units))
	if start >= end {
		return ""
	}
	return string(utf16.Decode(units[start:end]))
}

// utf16Tail is slice(-n): the last n code units.
func utf16Tail(text string, n int) string {
	length := utf16Len(text)
	return utf16Slice(text, max(0, length-n), length)
}

// padStart is String(value).padStart(width) with spaces.
func padStart(text string, width int) string {
	if gap := width - utf16Len(text); gap > 0 {
		return strings.Repeat(" ", gap) + text
	}
	return text
}

// padEnd is padEnd(width) with spaces.
func padEnd(text string, width int) string {
	if gap := width - utf16Len(text); gap > 0 {
		return text + strings.Repeat(" ", gap)
	}
	return text
}

// cut fits text to width: an over-long one keeps width-1 units and "…".
func cut(text string, width int) string {
	if utf16Len(text) > width {
		text = utf16Slice(text, 0, width-1) + "…"
	}
	return padEnd(text, width)
}

// num is Number.prototype.toString.
func num(value float64) string { return jsvalue.Number(value) }

// fixed is Number.prototype.toFixed(digits). An exact binary half rounds to
// even here where Node rounded it up: the ruled, listed tie delta.
func fixed(value float64, digits int) string {
	if math.IsNaN(value) || math.Abs(value) >= 1e21 {
		return num(value)
	}
	text := strconv.FormatFloat(value, 'f', digits, 64)
	if value == 0 {
		return strings.TrimPrefix(text, "-") // (-0).toFixed is unsigned
	}
	return text
}

// fixedNum is +value.toFixed(digits): the rounded number itself.
func fixedNum(value float64, digits int) float64 {
	rounded, err := strconv.ParseFloat(fixed(value, digits), 64)
	if err != nil {
		return value
	}
	return rounded
}

// round is Math.round: halves go toward +Infinity.
func round(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	floor := math.Floor(value)
	switch {
	case value-floor < 0.5:
		return floor
	case floor == -1:
		return math.Copysign(0, -1) // Math.round(-0.4) is -0
	}
	return floor + 1
}

// dollars is the report's $ column: whole dollars from $100, cents below.
func dollars(value float64) string {
	if value >= 100 {
		return "$" + fixed(value, 0)
	}
	return "$" + fixed(value, 2)
}

// pctText is v as a share of total, one decimal; "0.0%" of nothing.
func pctText(value, total float64) string {
	if total == 0 || math.IsNaN(total) {
		return "0.0%"
	}
	return fixed(100*value/total, 1) + "%"
}

// millions is a token count in millions, one decimal.
func millions(value float64) string { return fixed(value/1e6, 1) + "M" }

var (
	modelPrefix = regexp.MustCompile(`^claude-`)
	modelDate   = regexp.MustCompile(`-\d{8}$`)
	codexPrefix = regexp.MustCompile(`^gpt-`)
)

// modelStem drops a Claude id's family prefix and snapshot date.
func modelStem(model string) string {
	if model == "" {
		model = "?"
	}
	return modelDate.ReplaceAllString(modelPrefix.ReplaceAllString(model, ""), "")
}

// codexShort drops a Codex id's gpt- prefix.
func codexShort(model string) string {
	if model == "" {
		model = "?"
	}
	return codexPrefix.ReplaceAllString(model, "")
}

// ttlMix names where a run's cache writes went: 5m, 1h, the 1-hour share
// when it wrote both, "—" with no writes.
func ttlMix(write5m, write1h float64) string {
	writes := write5m + write1h
	switch {
	case writes == 0:
		return "—"
	case write1h == 0:
		return "5m"
	case write5m == 0:
		return "1h"
	}
	return "1h " + num(math.Min(99, math.Max(1, round(100*write1h/writes)))) + "%"
}

// grouped is Number.prototype.toLocaleString("en-US"): thousands grouped,
// at most three decimals.
func grouped(value float64) string {
	text := fixed(value, 3)
	if strings.Contains(text, ".") {
		text = strings.TrimSuffix(strings.TrimRight(text, "0"), ".")
	}
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	whole, fraction, hasFraction := strings.Cut(text, ".")
	var out strings.Builder
	out.WriteString(sign)
	for i, digit := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	if hasFraction {
		out.WriteString("." + fraction)
	}
	return out.String()
}

// isoTime is Date.prototype.toISOString for a millisecond instant.
func isoTime(ms float64) string {
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z")
}

// jsSpaceClass is JavaScript's \s: Go's lacks \v and the Unicode spaces.
const jsSpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsRegexp compiles a JavaScript pattern whose \s and \S stand outside a
// character class, with JavaScript's whitespace.
func jsRegexp(pattern string) *regexp.Regexp {
	pattern = strings.ReplaceAll(pattern, `\s`, `[`+jsSpaceClass+`]`)
	pattern = strings.ReplaceAll(pattern, `\S`, `[^`+jsSpaceClass+`]`)
	return regexp.MustCompile(pattern)
}

var jsSpaces = jsRegexp(`\s+`)

// collapseSpaces is replace(/\s+/g, " ").
func collapseSpaces(text string) string { return jsSpaces.ReplaceAllString(text, " ") }

// jsTrim is String.prototype.trim.
func jsTrim(text string) string {
	return strings.TrimFunc(text, isJSSpace)
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// dateParse is Date.parse in milliseconds, over the one timestamp parser.
func dateParse(text string, loc *time.Location) (float64, bool) {
	at, ok := transcript.ParseTimestamp(text, loc)
	if !ok {
		return math.NaN(), false
	}
	return float64(at.UnixMilli()), true
}
