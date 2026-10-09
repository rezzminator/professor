// Package pyvalue reads and writes JSON values the way Python does: str() and
// repr() of a decoded value, json.dumps(value, ensure_ascii=False), float repr,
// truthiness, and the str methods whose whitespace and line boundaries are
// Unicode's (isspace, strip, splitlines). Readers whose output is defined by
// what a Python program printed (the transcript digest) use it so that output
// stays byte-identical; jsvalue is its JavaScript twin.
package pyvalue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rezzminator/professor/pfm/internal/jsvalue"
)

// String is raw's value when it is a JSON string.
func String(raw json.RawMessage) (string, bool) {
	raw = trim(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return text, true
}

// IsNull reports an absent member or a JSON null: Python's None.
func IsNull(raw json.RawMessage) bool {
	raw = trim(raw)
	return len(raw) == 0 || string(raw) == "null"
}

// Bool is bool(value): Python truthiness of a decoded value: None, False, a zero, an
// empty string, list or object are false.
func Bool(raw json.RawMessage) bool {
	raw = trim(raw)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case 'n', 'f':
		return false
	case 't':
		return true
	case '"':
		text, _ := String(raw)
		return text != ""
	case '[':
		var list []json.RawMessage
		return json.Unmarshal(raw, &list) == nil && len(list) != 0
	case '{':
		members, _ := jsvalue.InsertionMembers(raw)
		return len(members) != 0
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	return err != nil || value != 0
}

// Str is Python's str() of a decoded value: a string as itself, None, True or
// False, a number as Python prints it, a list or object as its repr. An absent
// member is None.
func Str(raw json.RawMessage) string {
	if text, ok := String(raw); ok {
		return text
	}
	return Repr(raw)
}

// Repr is Python's repr() of a decoded value.
func Repr(raw json.RawMessage) string {
	raw = trim(raw)
	if len(raw) == 0 {
		return "None"
	}
	switch raw[0] {
	case 'n':
		return "None"
	case 't':
		return "True"
	case 'f':
		return "False"
	case '"':
		text, _ := String(raw)
		return ReprString(text)
	case '[':
		var list []json.RawMessage
		_ = json.Unmarshal(raw, &list)
		parts := make([]string, len(list))
		for index, item := range list {
			parts[index] = Repr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case '{':
		members, _ := jsvalue.InsertionMembers(raw)
		parts := make([]string, len(members))
		for index, member := range members {
			parts[index] = ReprString(member.Key) + ": " + Repr(member.Value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return numberRepr(raw, "inf")
}

// ReprString is repr() of a str: single quotes unless the text holds a single
// quote and no double one; a non-printable character escaped as \xNN, \uNNNN
// or \UNNNNNNNN.
func ReprString(text string) string {
	quote := '\''
	if strings.ContainsRune(text, '\'') && !strings.ContainsRune(text, '"') {
		quote = '"'
	}
	var out strings.Builder
	out.WriteRune(quote)
	for _, r := range text {
		switch {
		case r == quote || r == '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case r == '\t':
			out.WriteString(`\t`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r < ' ' || r == 0x7f:
			fmt.Fprintf(&out, `\x%02x`, r)
		case r < 0x7f || unicode.IsPrint(r):
			out.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&out, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			fmt.Fprintf(&out, `\U%08x`, r)
		}
	}
	out.WriteRune(quote)
	return out.String()
}

// Dumps is json.dumps(value, ensure_ascii=False): members in the order
// written, ", " and ": " separators, non-ASCII text kept, a float in Python's
// repr. An absent member is null.
func Dumps(raw json.RawMessage) string {
	raw = trim(raw)
	if len(raw) == 0 {
		return "null"
	}
	switch raw[0] {
	case 'n', 't', 'f':
		return string(raw)
	case '"':
		text, _ := String(raw)
		return quoteJSON(text)
	case '[':
		var list []json.RawMessage
		_ = json.Unmarshal(raw, &list)
		parts := make([]string, len(list))
		for index, item := range list {
			parts[index] = Dumps(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case '{':
		members, _ := jsvalue.InsertionMembers(raw)
		parts := make([]string, len(members))
		for index, member := range members {
			parts[index] = quoteJSON(member.Key) + ": " + Dumps(member.Value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return numberRepr(raw, "Infinity")
}

// quoteJSON is json.dumps's string encoder with ensure_ascii=False: only the
// quote, the backslash and the C0 controls are escaped.
func quoteJSON(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if r < ' ' {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

// numberRepr prints a JSON number literal as Python holds it: an integer exactly
// (json.loads makes an int of a literal without fraction or exponent), any
// other as a float's repr; infinity names an overflowed float.
func numberRepr(raw json.RawMessage, infinity string) string {
	literal := string(raw)
	if !strings.ContainsAny(literal, ".eE") {
		value, ok := new(big.Int).SetString(literal, 10)
		if ok {
			return value.String()
		}
		return literal
	}
	value, err := strconv.ParseFloat(literal, 64)
	if err != nil && !math.IsInf(value, 0) {
		return literal
	}
	return FloatRepr(value, infinity)
}

// FloatRepr is repr(float): the shortest round-trip digits, fixed notation
// while the decimal point sits within 16 places (with ".0" on a whole number),
// exponent notation with a two-digit minimum exponent beyond. infinity names
// +Inf ("inf" for repr, "Infinity" for json.dumps).
func FloatRepr(value float64, infinity string) string {
	switch {
	case math.IsInf(value, 1):
		return infinity
	case math.IsInf(value, -1):
		return "-" + infinity
	case math.IsNaN(value):
		return "nan"
	case value == 0:
		if math.Signbit(value) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if value < 0 {
		sign, value = "-", -value
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponentText, _ := strings.Cut(scientific, "e")
	exponent, _ := strconv.Atoi(exponentText)
	digits := strings.Replace(mantissa, ".", "", 1)
	point := exponent + 1
	if point > -4 && point <= 16 {
		switch {
		case point <= 0:
			return sign + "0." + strings.Repeat("0", -point) + digits
		case point >= len(digits):
			return sign + digits + strings.Repeat("0", point-len(digits)) + ".0"
		default:
			return sign + digits[:point] + "." + digits[point:]
		}
	}
	out := sign + digits[:1]
	if len(digits) > 1 {
		out += "." + digits[1:]
	}
	expSign := "+"
	if exponent < 0 {
		expSign, exponent = "-", -exponent
	}
	return fmt.Sprintf("%se%s%02d", out, expSign, exponent)
}

// ParseInt is int(text) for a decimal literal: surrounding whitespace, a sign,
// and single underscores between digits are allowed.
func ParseInt(text string) (int, bool) {
	text = Strip(text)
	body := strings.TrimLeft(text, "+-")
	if len(text)-len(body) > 1 || body == "" || body[0] == '_' || body[len(body)-1] == '_' ||
		strings.Contains(body, "__") {
		return 0, false
	}
	value, err := strconv.Atoi(text[:len(text)-len(body)] + strings.ReplaceAll(body, "_", ""))
	if err != nil {
		return 0, false
	}
	return value, true
}

// IsSpace is str.isspace for one character: Unicode's whitespace plus the
// information separators U+001C–U+001F, which Python counts and Go does not.
func IsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// Strip is str.strip().
func Strip(text string) string { return strings.TrimFunc(text, IsSpace) }

// LStrip is str.lstrip().
func LStrip(text string) string { return strings.TrimLeftFunc(text, IsSpace) }

// RStrip is str.rstrip().
func RStrip(text string) string { return strings.TrimRightFunc(text, IsSpace) }

// OneLine is re.sub(r"\s+", " ", text).strip(): every run of whitespace one
// space, none at either end.
func OneLine(text string) string {
	var out strings.Builder
	space := false
	for _, r := range text {
		if IsSpace(r) {
			space = true
			continue
		}
		if space && out.Len() != 0 {
			out.WriteByte(' ')
		}
		space = false
		out.WriteRune(r)
	}
	return out.String()
}

// SplitLines is str.splitlines(): lines without their terminators, split at
// \n, \r, \r\n, \v, \f, U+001C–U+001E, U+0085, U+2028 and U+2029; a trailing
// terminator opens no empty last line.
func SplitLines(text string) []string {
	var lines []string
	start := 0
	for index := 0; index < len(text); {
		r, width := utf8.DecodeRuneInString(text[index:])
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			lines = append(lines, text[start:index])
			start = index + width
		case '\r':
			lines = append(lines, text[start:index])
			if index+1 < len(text) && text[index+1] == '\n' {
				width++
			}
			start = index + width
		}
		index += width
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

func trim(raw json.RawMessage) json.RawMessage {
	return bytes.TrimSpace(raw)
}
