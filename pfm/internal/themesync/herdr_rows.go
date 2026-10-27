package themesync

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// The sidebar row entries that name a pfm indicator token (herdr's
// IndicatorTokens as `$pfm_working` and the rest) are styled with the Claude
// theme's hue for that state: Herdr paints a custom token in the colour its
// row entry gives it, so themesync keeps `fg` equal to the derived hue.

const (
	herdrSidebarTable = "ui.sidebar"
	herdrTokenPrefix  = "$"
)

// herdrRowEntry is one sidebar row entry naming an indicator token: its byte
// range in the file, the token (without the `$`), and for an inline table the
// keys it carries.
type herdrRowEntry struct {
	start, end int
	token      string
	inline     bool
	keys       []string
}

// herdrSidebarEntries scans a Herdr config the way TOML reads it (comments,
// basic, literal and multi-line strings, arrays, inline tables) and returns
// every row entry that names an indicator token inside a `[ui.sidebar]` or
// `[ui.sidebar.*]` table: a bare array element exactly `"$pfm_X"`, or an
// inline table with no nested table whose `token` is `"$pfm_X"`. The pfm
// theme block is skipped. A string that never closes is an error naming its
// line, never an empty result.
func herdrSidebarEntries(content string) ([]herdrRowEntry, error) {
	known := herdrIndicatorSet()
	blockStart, blockEnd, hasBlock := herdrBlockRange(content)
	var entries []herdrRowEntry
	table, depth, lineStart := "", 0, true
	for index := 0; index < len(content); {
		if hasBlock && index >= blockStart && index < blockEnd {
			index, table, depth, lineStart = blockEnd, "", 0, true
			continue
		}
		if lineStart && depth == 0 {
			lineEnd := strings.IndexByte(content[index:], '\n')
			if lineEnd < 0 {
				lineEnd = len(content) - index
			}
			if match := herdrHeaderPattern.FindStringSubmatch(
				strings.TrimRight(content[index:index+lineEnd], "\r"),
			); match != nil {
				table = strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "").Replace(match[1])
				index += lineEnd
				lineStart = false
				continue
			}
		}
		lineStart = false
		eligible := table == herdrSidebarTable || strings.HasPrefix(table, herdrSidebarTable+".")
		switch character := content[index]; character {
		case '\n':
			lineStart = true
			index++
		case '#':
			lineEnd := strings.IndexByte(content[index:], '\n')
			if lineEnd < 0 {
				lineEnd = len(content) - index
			}
			index += lineEnd
		case '"', '\'':
			end, value, plain, err := herdrScanString(content, index)
			if err != nil {
				return nil, err
			}
			if eligible && depth >= 1 && plain && known[strings.TrimPrefix(value, herdrTokenPrefix)] &&
				strings.HasPrefix(value, herdrTokenPrefix) {
				entries = append(
					entries,
					herdrRowEntry{start: index, end: end, token: strings.TrimPrefix(value, herdrTokenPrefix)},
				)
			}
			index = end
		case '[':
			depth++
			index++
		case ']':
			if depth > 0 {
				depth--
			}
			index++
		case '{':
			entry, end, err := herdrScanInline(content, index, known)
			if err != nil {
				return nil, err
			}
			if eligible && entry != nil {
				entries = append(entries, *entry)
			}
			index = end
		default:
			index++
		}
	}
	return entries, nil
}

func herdrIndicatorSet() map[string]bool {
	known := map[string]bool{}
	for _, token := range herdr.IndicatorTokens() {
		known[token] = true
	}
	return known
}

func herdrLineOf(content string, index int) int {
	return strings.Count(content[:index], "\n") + 1
}

// herdrScanString reads the TOML string starting at content[start] (a basic,
// literal, or either multi-line kind) and returns the index just past it, its
// raw text when it is a single-line string without escapes (plain), and an
// error naming the line when it never closes.
func herdrScanString(content string, start int) (end int, value string, plain bool, err error) {
	quote := content[start]
	triple := strings.Repeat(string(quote), 3)
	if strings.HasPrefix(content[start:], triple) {
		for index := start + 3; index < len(content); index++ {
			switch {
			case quote == '"' && content[index] == '\\':
				index++
			case strings.HasPrefix(content[index:], triple):
				return index + 3, "", false, nil
			}
		}
		return 0, "", false, fmt.Errorf(
			"themesync: herdr config: multi-line string opened at line %d never closes",
			herdrLineOf(content, start),
		)
	}
	escaped := false
	for index := start + 1; index < len(content); index++ {
		switch character := content[index]; {
		case character == '\n':
			return 0, "", false, fmt.Errorf(
				"themesync: herdr config: string opened at line %d never closes",
				herdrLineOf(content, start),
			)
		case quote == '"' && character == '\\':
			escaped = true
			index++
		case character == quote:
			if escaped {
				return index + 1, "", false, nil
			}
			return index + 1, content[start+1 : index], true, nil
		}
	}
	return 0, "", false, fmt.Errorf(
		"themesync: herdr config: string opened at line %d never closes",
		herdrLineOf(content, start),
	)
}

// herdrScanInline reads the inline table starting at content[start] and
// returns the index just past its closing brace; the entry is non-nil only for
// a table with no nested braces whose `token` names an indicator token.
func herdrScanInline(content string, start int, known map[string]bool) (entry *herdrRowEntry, end int, err error) {
	braces, brackets, nested := 0, 0, false
	segmentStart := start + 1
	var segments []string
	for index := start; index < len(content); {
		switch content[index] {
		case '"', '\'':
			stringEnd, _, _, err := herdrScanString(content, index)
			if err != nil {
				return nil, 0, err
			}
			index = stringEnd
			continue
		case '#':
			lineEnd := strings.IndexByte(content[index:], '\n')
			if lineEnd < 0 {
				lineEnd = len(content) - index
			}
			index += lineEnd
			continue
		case '{':
			braces++
			if braces > 1 {
				nested = true
			}
		case '[':
			brackets++
		case ']':
			brackets--
		case ',':
			if braces == 1 && brackets == 0 {
				segments = append(segments, content[segmentStart:index])
				segmentStart = index + 1
			}
		case '}':
			braces--
			if braces == 0 {
				segments = append(segments, content[segmentStart:index])
				if nested {
					return nil, index + 1, nil
				}
				return herdrInlineEntry(start, index+1, segments, known), index + 1, nil
			}
		}
		index++
	}
	return nil, 0, fmt.Errorf(
		"themesync: herdr config: inline table opened at line %d never closes",
		herdrLineOf(content, start),
	)
}

func herdrInlineEntry(start, end int, segments []string, known map[string]bool) *herdrRowEntry {
	entry := &herdrRowEntry{start: start, end: end, inline: true}
	named := false
	for _, segment := range segments {
		key, value, found := strings.Cut(segment, "=")
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		if !found || key == "" {
			continue
		}
		entry.keys = append(entry.keys, key)
		value = strings.TrimSpace(value)
		if key != "token" || len(value) < 2 || value[0] != value[len(value)-1] ||
			(value[0] != '"' && value[0] != '\'') {
			continue
		}
		if name, ok := strings.CutPrefix(value[1:len(value)-1], herdrTokenPrefix); ok && known[name] {
			entry.token, named = name, true
		}
	}
	if !named {
		return nil
	}
	return entry
}

// errHerdrNoHue is the herdrRowsStyled failure of a row naming a token the
// applied theme has no hue for: a read-back takes it as DRIFT (a state written
// before that hue existed heals on the next apply), an apply as an error.
var errHerdrNoHue = errors.New("the applied theme carries no hue for it")

// herdrRowsStyled returns content with every sidebar row entry that names an
// indicator token rewritten to `{ token = "$pfm_X", fg = "#hex" }`, the hex
// being hues[X]; every other byte stays. A file naming no indicator token
// comes back identical; an entry whose hue is empty is an error naming it.
func herdrRowsStyled(content string, hues map[string]string) (string, error) {
	return herdrRewriteRows(content, func(entry herdrRowEntry) (string, bool, error) {
		hue := hues[entry.token]
		if hue == "" {
			return "", false, fmt.Errorf(
				"themesync: herdr sidebar row names %s%s but %w",
				herdrTokenPrefix, entry.token, errHerdrNoHue,
			)
		}
		return fmt.Sprintf("{ token = %q, fg = %q }", herdrTokenPrefix+entry.token, hue), true, nil
	})
}

// herdrRowsBare undoes herdrRowsStyled: every inline entry that is exactly a
// token and an fg goes back to the bare string `"$pfm_X"`.
func herdrRowsBare(content string) (string, error) {
	return herdrRewriteRows(content, func(entry herdrRowEntry) (string, bool, error) {
		if !entry.inline || len(entry.keys) != 2 || !slices.Contains(entry.keys, "token") ||
			!slices.Contains(entry.keys, "fg") {
			return "", false, nil
		}
		return fmt.Sprintf("%q", herdrTokenPrefix+entry.token), true, nil
	})
}

func herdrRewriteRows(content string, rewrite func(herdrRowEntry) (string, bool, error)) (string, error) {
	entries, err := herdrSidebarEntries(content)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	last := 0
	for _, entry := range entries {
		replacement, ok, err := rewrite(entry)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		out.WriteString(content[last:entry.start])
		out.WriteString(replacement)
		last = entry.end
	}
	out.WriteString(content[last:])
	return out.String(), nil
}
