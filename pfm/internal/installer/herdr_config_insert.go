package installer

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// herdrTableHeaderAny matches any table header line, where a top-level key
// can go just before.
var herdrTableHeaderAny = regexp.MustCompile(`(?m)^[ \t]*\[`)

// herdrConfigTableHeader matches the header line of table, each dotted segment bare
// or quoted, a trailing comment allowed.
func herdrConfigTableHeader(table []string) *regexp.Regexp {
	segments := make([]string, 0, len(table))
	for _, segment := range table {
		quoted := regexp.QuoteMeta(segment)
		segments = append(segments, `(?:`+quoted+`|"`+quoted+`"|'`+quoted+`')`)
	}
	return regexp.MustCompile(
		`(?m)^\s*\[\s*` + strings.Join(segments, `\s*\.\s*`) + `\s*\][ \t]*(?:#[^\r\n]*)?(?:\r?\n|$)`,
	)
}

// herdrManagedBlock matches one block pfm owns by its markers (theme, shell,
// keys), which its own step rewrites whole: nothing else goes inside.
var herdrManagedBlock = regexp.MustCompile(`(?m)^# >>> pfm [^\n]*\n(?:[^\n]*\n)*?# <<< pfm [^\n]*$`)

// insertHerdrConfigLines is the one door adding keys to a Herdr config.toml
// as text, every other byte kept: lines (key = value lines, comments allowed)
// go under table's existing header, before the first table (and the comment
// lines just above it) for the top level (table empty), else under a new
// header at the end; never inside a block pfm owns by its markers. The lines
// keep their LF endings in a CRLF file: each pfm block's owner finds it again
// by its exact bytes. Each candidate is proven by decoding: the config
// afterwards must be the config before plus exactly lines' keys in table, so a
// header-looking line inside a multiline string never takes them.
func insertHerdrConfigLines(original string, table []string, lines string) (string, error) {
	before, err := decodeHerdrConfig(original)
	if err != nil {
		return "", err
	}
	added, err := decodeHerdrConfig(lines)
	if err != nil {
		return "", fmt.Errorf("render %s: %w", lines, err)
	}
	want, err := herdrConfigWith(before, table, added)
	if err != nil {
		return "", err
	}
	valid := func(candidate string) bool {
		after, err := decodeHerdrConfig(candidate)
		return err == nil && reflect.DeepEqual(after, want)
	}
	managed := herdrManagedBlock.FindAllStringIndex(original, -1)
	inManaged := func(at int) bool {
		for _, block := range managed {
			if at > block[0] && at < block[1] {
				return true
			}
		}
		return false
	}
	var candidates []string
	if len(table) == 0 {
		for _, location := range herdrTableHeaderAny.FindAllStringIndex(original, -1) {
			at := herdrCommentRunStart(original, location[0])
			if inManaged(at) {
				continue
			}
			candidates = append(candidates, original[:at]+lines+original[at:])
		}
		candidates = append(candidates, lines+original)
	} else {
		for _, location := range herdrConfigTableHeader(table).FindAllStringIndex(original, -1) {
			if inManaged(location[0]) {
				continue
			}
			prefix := original[:location[1]]
			if !strings.HasSuffix(prefix, "\n") {
				prefix += "\n"
			}
			candidates = append(candidates, prefix+lines+original[location[1]:])
		}
		appended := original
		if appended != "" && !strings.HasSuffix(appended, "\n") {
			appended += "\n"
		}
		candidates = append(candidates, appended+"\n["+strings.Join(table, ".")+"]\n"+lines)
	}
	for _, candidate := range candidates {
		if valid(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot add %s without rewriting the existing %s table",
		strings.Join(slices.Sorted(maps.Keys(added)), ", "), herdrTableName(table))
}

// herdrCommentRunStart is the start of the comment lines directly above the
// line starting at at, at itself when there are none: a header's comments,
// a pfm block's opening marker among them, stay with it.
func herdrCommentRunStart(text string, at int) int {
	for at > 0 {
		lineStart := strings.LastIndex(text[:at-1], "\n") + 1
		if !strings.HasPrefix(strings.TrimSpace(text[lineStart:at]), "#") {
			return at
		}
		at = lineStart
	}
	return at
}

// herdrConfigWith is config with values set in table, a copy along the path.
func herdrConfigWith(config map[string]any, table []string, values map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(config))
	maps.Copy(result, config)
	if len(table) == 0 {
		maps.Copy(result, values)
		return result, nil
	}
	child := map[string]any{}
	if existing, present := config[table[0]]; present {
		var isTable bool
		if child, isTable = existing.(map[string]any); !isTable {
			return nil, fmt.Errorf("%s is not a table", table[0])
		}
	}
	nested, err := herdrConfigWith(child, table[1:], values)
	if err != nil {
		return nil, fmt.Errorf("%s.%w", table[0], err)
	}
	result[table[0]] = nested
	return result, nil
}

func herdrTableName(table []string) string {
	if len(table) == 0 {
		return "top-level"
	}
	return strings.Join(table, ".")
}
