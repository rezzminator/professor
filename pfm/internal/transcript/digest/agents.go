package digest

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// agentRow is one sub-agent's line and the stamp it sorts by.
type agentRow struct {
	first *time.Time
	text  string
}

// sessionAgents lists a top-level Claude session's sub-agents, nested
// workflow agents included: each by its id, meta, span, records, size and
// tool calls, the earliest first.
func sessionAgents(path string) (string, error) {
	name := filepath.Base(path)
	if filepath.Base(filepath.Dir(path)) == "subagents" || strings.HasPrefix(name, "agent-") ||
		strings.HasPrefix(name, rolloutPrefix) {
		return "", usageFailure("agents takes a top-level Claude session; %s is a sub-agent or Codex transcript", path)
	}
	sid := strings.TrimSuffix(name, ".jsonl")
	folder := transcript.SubagentsDir(path)
	lines := []string{"SESSION " + sid + " · " + path}
	info, err := os.Stat(folder)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return strings.Join(append(lines, "AGENTS 0 — no subagents directory at "+folder), "\n") + "\n", nil
	case err != nil:
		return "", runtimeFailure("subagents stat failed at %s: %v", folder, err)
	case !info.IsDir():
		return "", runtimeFailure("subagents path is not a directory: %s", folder)
	}
	files, err := transcript.SubagentTranscripts(folder)
	if err != nil {
		return "", runtimeFailure("subagents enumeration failed at %s: %v", folder, err)
	}
	var rows []agentRow
	for _, file := range uniqueResolved(files) {
		rows = append(rows, readAgentRow(file))
	}
	sort.SliceStable(rows, func(left, right int) bool {
		a, b := rows[left], rows[right]
		switch {
		case a.first != nil && b.first != nil && !a.first.Equal(*b.first):
			return a.first.Before(*b.first)
		case (a.first == nil) != (b.first == nil):
			return a.first != nil
		}
		return a.text < b.text
	})
	lines = append(lines, fmt.Sprintf("AGENTS %d", len(rows)))
	for _, row := range rows {
		lines = append(lines, row.text)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// readAgentRow reads one sub-agent transcript in one streamed pass: its
// records, first and last stamps and tool_use blocks; no digest is built.
func readAgentRow(path string) agentRow {
	records, calls := 0, 0
	var first, last *time.Time
	_, _, err := transcript.ReadLines(path, 0, func(line []byte, _ bool) {
		if len(bytes.Trim(line, " \t\n\r\v\f")) == 0 {
			return
		}
		records++
		record, ok := transcript.DecodeClaudeRecord(line)
		if !ok {
			return
		}
		if at, ok := parseStamp(stampOf(record.Timestamp)); ok {
			if first == nil || at.Before(*first) {
				first = &at
			}
			if last == nil || at.After(*last) {
				last = &at
			}
		}
		if kind, _ := record.Kind(); kind != transcript.RoleAssistant {
			return
		}
		message := record.DecodeMessage()
		if message == nil {
			return
		}
		blocks, _ := transcript.DecodeContent(message.Content)
		for index := range blocks {
			if blocks[index].IsObject() && blocks[index].Kind() == "tool_use" {
				calls++
			}
		}
	})
	if err != nil {
		return agentRow{text: filepath.Base(path) + " · READ ERROR " + err.Error()}
	}
	id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "agent-"), ".jsonl")
	span := "no timestamps"
	if first != nil {
		span = first.Format("2006-01-02 15:04:05") + "Z → " + last.Format("2006-01-02 15:04:05") + "Z"
	}
	info, err := os.Stat(path)
	if err != nil {
		return agentRow{text: filepath.Base(path) + " · READ ERROR " + err.Error()}
	}
	return agentRow{first: first, text: fmt.Sprintf("%s · %s · %s · %d records · %s · %d calls", id,
		agentMetaText(path), span, records, sizeText(int(info.Size())), calls)}
}

// agentMetaText is a sub-agent's type and description from its meta file, or
// the META ERROR that kept them unread.
func agentMetaText(path string) string {
	meta := transcript.SubagentMetaPath(path)
	if info, err := os.Stat(meta); err != nil || !info.Mode().IsRegular() {
		return "META ERROR missing " + meta
	}
	data, err := transcript.ReadSubagentMeta(meta)
	if err != nil {
		return "META ERROR " + err.Error()
	}
	kind, description := data.AgentType, data.Description
	if kind == "" {
		kind = "type ?"
	}
	if description == "" {
		description = "no description"
	}
	return kind + " · " + description
}
