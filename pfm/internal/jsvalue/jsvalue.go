// Package jsvalue reads and writes JSON the way JavaScript does: an object's
// members in property order (integer keys ascending, then insertion order), a
// number as Number.prototype.toString prints it, and JSON.stringify's escapes.
// Readers whose output is defined by what a JavaScript engine printed (the
// token audit's report and JSON, a Codex source object's first member) use it
// so that output stays byte-identical.
package jsvalue

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Member is one property of a decoded object.
type Member struct {
	Key   string
	Value json.RawMessage
}

// Members decodes raw as an object and returns its members in JavaScript
// property order; a key given twice keeps its first place and its last value.
// ok is false when raw is no object.
func Members(raw json.RawMessage) (members []Member, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	place := map[string]int{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		if at, seen := place[key]; seen {
			members[at].Value = value
			continue
		}
		place[key] = len(members)
		members = append(members, Member{Key: key, Value: value})
	}
	SortKeys(members, func(member Member) string { return member.Key })
	return members, true
}

// SortKeys orders items by JavaScript property order of key(item): canonical
// array indices ascending first, every other key keeping its place.
func SortKeys[T any](items []T, key func(T) string) {
	sort.SliceStable(items, func(left, right int) bool {
		leftIndex, leftIsIndex := arrayIndex(key(items[left]))
		rightIndex, rightIsIndex := arrayIndex(key(items[right]))
		if leftIsIndex && rightIsIndex {
			return leftIndex < rightIndex
		}
		return leftIsIndex && !rightIsIndex
	})
}

// arrayIndex reports key as a canonical array index ("0", "17", never "07").
func arrayIndex(key string) (uint64, bool) {
	index, err := strconv.ParseUint(key, 10, 32)
	if err != nil || strconv.FormatUint(index, 10) != key || index == 1<<32-1 {
		return 0, false
	}
	return index, true
}

// Number is Number.prototype.toString. encoding/json already writes a finite
// float64 the way ECMAScript prints it (shortest round-trip digits, fixed
// notation from 1e-6 up to 1e21, exponent form outside it, "e-7" never
// "e-07"); Number adds only what JSON cannot say: -0 prints "0", and the
// non-finite values print their JavaScript names.
func Number(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	case value == 0:
		return "0"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		// json.Marshal refuses only NaN and ±Inf, both answered above
		return strconv.FormatFloat(value, 'g', -1, 64)
	}
	return string(encoded)
}

// JSONNumber is a number as JSON.stringify writes it: null when not finite.
func JSONNumber(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "null"
	}
	return Number(value)
}

// QuoteString is JSON.stringify of a string: ", \ and the control characters
// escaped, \b \f \n \r \t by name, everything else as written.
func QuoteString(text string) string {
	var out strings.Builder
	out.Grow(len(text) + 2)
	out.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

// StringifyRaw is JSON.writeParsed(JSON.parse(raw)): raw re-written compactly with
// JavaScript's member order, number form and escapes.
func StringifyRaw(raw json.RawMessage) (string, error) {
	var out strings.Builder
	if err := writeParsed(&out, raw); err != nil {
		return "", err
	}
	return out.String(), nil
}

func writeParsed(out *strings.Builder, raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return errors.New("empty JSON value")
	}
	var failed error
	switch raw[0] {
	case '{':
		members, ok := Members(raw)
		if !ok {
			return fmt.Errorf("malformed JSON object")
		}
		WriteContainer(out, '{', '}', len(members), "", "", func(i int, _ string) {
			WriteKey(out, members[i].Key, "")
			if err := writeParsed(out, members[i].Value); err != nil && failed == nil {
				failed = err
			}
		})
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		WriteContainer(out, '[', ']', len(items), "", "", func(i int, _ string) {
			if err := writeParsed(out, items[i]); err != nil && failed == nil {
				failed = err
			}
		})
	case '"':
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		out.WriteString(QuoteString(text))
	case 't', 'f', 'n':
		out.Write(raw)
	default:
		value, err := strconv.ParseFloat(string(raw), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return fmt.Errorf("malformed JSON number %q: %w", raw, err)
		}
		out.WriteString(JSONNumber(value))
	}
	return failed
}

// WriteContainer writes an object or array of n members as JSON.stringify
// does: opening and closing around them, comma-separated, compact when indent is
// "", else each member on its own line under prefix plus indent and the close
// back at prefix; an empty container is the two alone. member writes
// member i, inner being its line's prefix.
func WriteContainer(
	out *strings.Builder,
	opening, closing byte,
	n int,
	indent, prefix string,
	member func(i int, inner string),
) {
	out.WriteByte(opening)
	if n == 0 {
		out.WriteByte(closing)
		return
	}
	inner := prefix + indent
	for i := range n {
		if i > 0 {
			out.WriteByte(',')
		}
		if indent != "" {
			out.WriteString("\n" + inner)
		}
		member(i, inner)
	}
	if indent != "" {
		out.WriteString("\n" + prefix)
	}
	out.WriteByte(closing)
}

// WriteKey writes an object member's key and its colon, a space after it when
// indented.
func WriteKey(out *strings.Builder, key, indent string) {
	out.WriteString(QuoteString(key))
	out.WriteByte(':')
	if indent != "" {
		out.WriteByte(' ')
	}
}
