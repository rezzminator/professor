package opencodegen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func generatedMarker(source string) string {
	return newMarker + " from " + source + "; do not edit — edit the source, then re-run: pfm opencode build"
}

func hasMarker(content string) bool {
	prefix := content
	if len(prefix) > 800 {
		prefix = prefix[:800]
	}
	return strings.Contains(prefix, newMarker) || strings.Contains(prefix, oldMarker)
}

func openCodeFlatName(rel string) string {
	return strings.ReplaceAll(strings.TrimSuffix(strings.ReplaceAll(rel, "\\", "/"), ".md"), "/", "-")
}

func swapOpenCodeCommands(text string, roster map[string]string) string {
	if len(roster) == 0 {
		return text
	}
	keys := make([]string, 0, len(roster))
	for key := range roster {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '/' || (i > 0 && isCommandPrefix(text[i-1])) {
			out.WriteByte(text[i])
			i++
			continue
		}
		matched := ""
		for _, key := range keys {
			name := strings.TrimPrefix(key, "/")
			end := i + 1 + len(name)
			if end > len(text) || text[i+1:end] != name {
				continue
			}
			if end < len(text) && isCommandSuffix(text[end]) {
				continue
			}
			matched = key
			break
		}
		if matched == "" {
			out.WriteByte(text[i])
			i++
			continue
		}
		out.WriteString(roster[matched])
		i += 1 + len(strings.TrimPrefix(matched, "/"))
	}
	return out.String()
}

func isCommandPrefix(value byte) bool {
	return value == '/' || value == '-' || value == '_' || value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func isCommandSuffix(value byte) bool { return isCommandPrefix(value) || value == ':' }

func parseOpenCodeFrontmatter(text string) (map[string]string, string, error) {
	lines := strings.Split(text, "\n")
	fields := map[string]string{}
	if len(lines) == 0 || lines[0] != "---" {
		return fields, text, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter starts with --- but has no closing fence")
	}
	for i := 1; i < end; i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.HasSuffix(value, ">") || strings.HasSuffix(value, "|") {
			style := value[len(value)-1]
			var block []string
			indent := -1
			for i+1 < end {
				next := lines[i+1]
				if strings.TrimSpace(next) == "" {
					block = append(block, "")
					i++
					continue
				}
				leading := len(next) - len(strings.TrimLeft(next, " \t"))
				if leading == 0 || (indent >= 0 && leading < indent) {
					break
				}
				if indent < 0 {
					indent = leading
				}
				block = append(block, next[indent:])
				i++
			}
			for len(block) > 0 && block[len(block)-1] == "" {
				block = block[:len(block)-1]
			}
			if style == '>' {
				value = strings.Join(block, " ")
			} else {
				value = strings.Join(block, "\n")
			}
		}
		if len(value) >= 2 && value[0] == value[len(value)-1] {
			switch value[0] {
			case '"':
				var unquoted string
				if err := json.Unmarshal([]byte(value), &unquoted); err != nil {
					return nil, "", fmt.Errorf("frontmatter field %s: %w", key, err)
				}
				value = unquoted
			case '\'':
				value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
			}
		}
		fields[key] = value
	}
	return fields, strings.Join(lines[end+1:], "\n"), nil
}
