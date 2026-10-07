package ui

import (
	"strings"
	"unicode"
)

// matchMask marks the runes of text that the search query lights up, so the
// operator sees WHY a row survived the filter. Each whitespace-separated token
// of the query is looked up on its own: a contiguous occurrence wins, else its
// letters are found in order (the same two rules refilter admits a row by).
// It returns nil when nothing lights up, so a caller can skip the split.
func matchMask(text, query string) []bool {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 || text == "" {
		return nil
	}
	runes := []rune(text)
	lowered := make([]rune, len(runes))
	for index, value := range runes {
		lowered[index] = unicode.ToLower(value)
	}
	mask := make([]bool, len(runes))
	lit := false
	for _, token := range tokens {
		pattern := []rune(token)
		if start := runeIndex(lowered, pattern); start >= 0 {
			for offset := range pattern {
				mask[start+offset] = true
			}
			lit = true
			continue
		}
		position := 0
		matched := make([]int, 0, len(pattern))
		for index, value := range lowered {
			if position < len(pattern) && value == pattern[position] {
				matched = append(matched, index)
				position++
			}
		}
		if position == len(pattern) {
			for _, index := range matched {
				mask[index] = true
			}
			lit = true
		}
	}
	if !lit {
		return nil
	}
	return mask
}

// runeIndex is strings.Index over rune slices: the first position of pattern
// in text, or -1.
func runeIndex(text, pattern []rune) int {
	if len(pattern) == 0 || len(pattern) > len(text) {
		return -1
	}
	for start := 0; start+len(pattern) <= len(text); start++ {
		found := true
		for offset, value := range pattern {
			if text[start+offset] != value {
				found = false
				break
			}
		}
		if found {
			return start
		}
	}
	return -1
}

// highlightSpans splits text into runs, painting the matched runes with lit and
// the rest with base. With no query, or no match, it is one run of base.
func highlightSpans(text, query string, base, lit tone) []span {
	mask := matchMask(text, query)
	if mask == nil {
		return []span{{text: text, paint: base}}
	}
	var spans []span
	var run strings.Builder
	current := mask[0]
	flush := func() {
		if run.Len() == 0 {
			return
		}
		paint := base
		if current {
			paint = lit
		}
		spans = append(spans, span{text: run.String(), paint: paint})
		run.Reset()
	}
	for index, value := range []rune(text) {
		if mask[index] != current {
			flush()
			current = mask[index]
		}
		run.WriteRune(value)
	}
	flush()
	return spans
}
