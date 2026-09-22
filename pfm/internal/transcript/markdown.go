package transcript

import (
	"fmt"
	"strings"
)

// Markdown writes a transcript's entries as the markdown `pfm chat read` hands a
// reader: each user and assistant turn and each compaction summary under its
// own heading, a tool call as a one-line note.
func Markdown(entries []Entry) string {
	var output strings.Builder
	for _, entry := range entries {
		switch entry.Role {
		case RoleUser:
			fmt.Fprintf(&output, "## USER\n\n%s\n\n", entry.Text)
		case RoleAssistant:
			fmt.Fprintf(&output, "## ASSISTANT\n\n%s\n\n", entry.Text)
		case RoleSummary:
			fmt.Fprintf(&output, "## [COMPACTION SUMMARY]\n\n%s\n\n", entry.Text)
		case RoleTool:
			fmt.Fprintf(&output, "> [tools: %s]\n\n", entry.Tool)
		}
	}
	return output.String()
}

// LastLines keeps the final count lines of value.
func LastLines(value string, count int) string {
	lines := strings.Split(value, "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}
