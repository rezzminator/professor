package harvestpy

import (
	"regexp"
	"strings"
)

// The assertions of TestHTMLConversionKeepsBlockStructure that read the
// markdown a conversion wrote: link targets, fenced blocks and table lines.

var linkTargetRE = regexp.MustCompile(`\]\(([^)\s]*)\)`)

// hasFencedBlock reports whether lines hold a fenced block whose content is
// exactly code, line by line, under the opening fence's own prefix (a list
// item's indent or a quote's marker), the fence longer than any backtick run
// in code.
func hasFencedBlock(lines []string, code string) bool {
	want := strings.Split(code, "\n")
	longest := 0
	for _, run := range regexp.MustCompile("`+").FindAllString(code, -1) {
		longest = max(longest, len(run))
	}
	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " >")
		prefix := line[:len(line)-len(trimmed)]
		if len(trimmed) < 3 || strings.Trim(trimmed, "`") != "" || len(trimmed) <= longest {
			continue
		}
		if index+len(want)+1 >= len(lines) {
			continue
		}
		matched := true
		for offset, content := range want {
			// A blank code line carries no trailing container indent; every
			// other line is byte-exact, its trailing spaces included.
			expected := prefix + content
			if content == "" {
				expected = strings.TrimRight(expected, " ")
			}
			if lines[index+1+offset] != expected {
				matched = false
				break
			}
		}
		if matched && lines[index+1+len(want)] == prefix+trimmed {
			return true
		}
	}
	return false
}

// hasTableLines reports whether lines hold want as consecutive lines, each
// without the list indent or quote marker in front of it.
func hasTableLines(lines, want []string) bool {
	for index := range lines {
		if index+len(want) > len(lines) {
			return false
		}
		matched := true
		for offset, line := range want {
			if strings.TrimLeft(lines[index+offset], " >") != line {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
