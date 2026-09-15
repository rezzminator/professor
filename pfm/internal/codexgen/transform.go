package codexgen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// TransformOptions contains only deterministic text substitutions. The
// compiler supplies the command roster discovered from source files.
type TransformOptions struct {
	ModelMap          map[string]string
	Commands          map[string]string
	ReplaceClaudeFile bool
}

var (
	frontmatterScalar = regexp.MustCompile(`^([A-Za-z-]+):\s*([>|])[-+]?\s*$`)
	frontmatterField  = regexp.MustCompile(`^([A-Za-z-]+):\s*(.*)$`)
)

// parseFrontmatter reads the two-fence YAML subset used by command files. It
// deliberately rejects malformed fences: silently treating a malformed
// header as body changes both the generated frontmatter and its invocation.
func parseFrontmatter(text string) (map[string]string, string, error) {
	lines := strings.Split(text, "\n")
	fields := make(map[string]string)
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
		if match := frontmatterScalar.FindStringSubmatch(line); match != nil {
			key, style := match[1], match[2]
			var chunk []string
			indent := -1
			for i+1 < end {
				next := lines[i+1]
				if strings.TrimSpace(next) == "" {
					chunk = append(chunk, "")
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
				chunk = append(chunk, next[indent:])
				i++
			}
			for len(chunk) > 0 && chunk[len(chunk)-1] == "" {
				chunk = chunk[:len(chunk)-1]
			}
			if style == ">" {
				fields[key] = strings.Join(chunk, " ")
			} else {
				fields[key] = strings.Join(chunk, "\n")
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		match := frontmatterField.FindStringSubmatch(line)
		if match == nil {
			// Claude frontmatter may carry richer YAML mappings (hooks, nested
			// tool policy) that this compiler neither consumes nor rewrites.
			// The incumbents ignore those continuation lines; rejecting them
			// would make a valid Claude agent impossible to mirror.
			continue
		}
		fields[match[1]] = match[2]
	}
	return fields, strings.Join(lines[end+1:], "\n"), nil
}

func transformMarkdown(text string, options TransformOptions) string {
	text = swapModels(text, options.ModelMap)
	text = swapCommands(text, options.Commands)
	if options.ReplaceClaudeFile {
		text = strings.ReplaceAll(text, "CLAUDE.md", "AGENTS.md")
	}
	return text
}

func swapModels(text string, modelMap map[string]string) string {
	if len(modelMap) == 0 {
		return text
	}
	keys := make([]string, 0, len(modelMap))
	for key := range modelMap {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, key := range keys {
		if key == "" {
			continue
		}
		pattern := regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(key) + `([^\w-]|$)`)
		text = pattern.ReplaceAllStringFunc(text, func(match string) string {
			start := 0
			if len(match) > 0 && !isModelChar(match[0]) {
				start = 1
			}
			end := len(match)
			if end > start && !isModelChar(match[end-1]) {
				end--
			}
			return match[:start] + modelMap[key] + match[end:]
		})
	}
	return text
}

func isModelChar(b byte) bool {
	return b == '_' || b == '-' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func swapCommands(text string, commands map[string]string) string {
	if len(commands) == 0 {
		return text
	}
	keys := make([]string, 0, len(commands))
	for key := range commands {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	var out strings.Builder
	for index := 0; index < len(text); {
		if text[index] != '/' || (index > 0 && isCommandPrefixChar(text[index-1])) {
			out.WriteByte(text[index])
			index++
			continue
		}
		matched := ""
		for _, key := range keys {
			name := strings.TrimPrefix(key, "/")
			end := index + 1 + len(name)
			if end > len(text) || text[index+1:end] != name {
				continue
			}
			if end < len(text) && isCommandSuffixChar(text[end]) {
				continue
			}
			matched = name
			break
		}
		if matched == "" {
			out.WriteByte(text[index])
			index++
			continue
		}
		replacement := commands[matched]
		if !strings.HasPrefix(replacement, "$") {
			replacement = "$" + replacement
		}
		out.WriteString(replacement)
		index += 1 + len(matched)
	}
	return out.String()
}

func isCommandPrefixChar(value byte) bool {
	return isASCIIWord(value) || value == '/' || value == '.' || value == '-'
}

func isCommandSuffixChar(value byte) bool {
	// A slash command is followed by whitespace, end-of-line, or punctuation
	// — never by another "/". Without this, a filesystem path that merely
	// starts with a command name (/dev/tty) gets rewritten right along with
	// the actual command invocation (/dev build pfm).
	return isASCIIWord(value) || value == ':' || value == '-' || value == '/'
}

func isASCIIWord(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
