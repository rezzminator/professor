package transcript

import (
	"bytes"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/pyvalue"
)

// Claude title record types.
const (
	customTitleType = "custom-title"
	agentNameType   = "agent-name"
)

// TitleOf is the title one Claude record names: a custom-title record's
// customTitle, an agent-name record's agentName, when it is non-empty. A
// chat's title is the LAST record TitleOf answers for, of either kind: Claude
// Code appends both over a chat's life (a rename writes its custom-title and
// agent-name together), so the newest names the chat.
func TitleOf(recordType, customTitle, agentName string) (string, bool) {
	switch {
	case recordType == customTitleType && customTitle != "":
		return customTitle, true
	case recordType == agentNameType && agentName != "":
		return agentName, true
	}
	return "", false
}

// Title is the title the record names (TitleOf over its type and members).
func (record *ClaudeRecord) Title() (string, bool) {
	kind, _ := record.Kind()
	custom, _ := pyvalue.String(record.CustomTitle)
	agent, _ := pyvalue.String(record.AgentName)
	return TitleOf(kind, custom, agent)
}

// LineTitle is the title one transcript line names.
func LineTitle(line []byte) (string, bool) {
	if !bytes.Contains(line, []byte(customTitleType)) && !bytes.Contains(line, []byte(agentNameType)) {
		return "", false
	}
	record, ok := DecodeClaudeRecord(line)
	if !ok {
		return "", false
	}
	return record.Title()
}

// TailTitle is the title the last title record of tail names, "" when none
// does: tail is a transcript's end, its first line possibly cut.
func TailTitle(tail []byte) string {
	for end := len(tail); end > 0; {
		start := bytes.LastIndexByte(tail[:end], '\n') + 1
		line := tail[start:end]
		end = start - 1
		if title, ok := LineTitle(line); ok {
			return title
		}
	}
	return ""
}

// FileTitle is the title of the Claude transcript at path: its last title
// record's, "" when it has none. A file that cannot be read is an error.
func FileTitle(path string) (string, error) {
	title := ""
	_, _, err := ReadLines(path, 0, func(line []byte, _ bool) {
		if name, ok := LineTitle(line); ok {
			title = name
		}
	})
	if err != nil {
		return "", fmt.Errorf("read transcript %q for its title: %w", path, err)
	}
	return title, nil
}
