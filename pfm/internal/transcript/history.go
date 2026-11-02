package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// historyUnparsedRole marks the HistoryMessage standing in for a row that is not JSON.
const historyUnparsedRole = "unparsed"

// HistoryMessage is one surviving user/assistant turn from a transcript tail,
// ready to print — the native port of history.sh's jq pipeline.
type HistoryMessage struct {
	Timestamp, Role, Text string
}

// FindHistory reproduces history.sh's pool search: each pool is tried in
// order, the newest-mtime `{pool}/{slug}/{sid}*.jsonl` match wins, and the
// first pool with any match short-circuits the rest.
func FindHistory(pools []string, slug, sid string) (string, error) {
	for _, pool := range pools {
		match, err := newestHistoryMatch(pool, slug, sid)
		if err != nil {
			return "", err
		}
		if match != "" {
			return match, nil
		}
	}
	return "", fmt.Errorf("no transcript matching sid '%s' under %s in any account pool", sid, slug)
}

// newestHistoryMatch returns the newest-mtime file under pool/slug matching
// sid*.jsonl, or "" when the pool has none. A glob candidate that fails to
// stat for a reason other than having vanished between glob and stat is a
// real error, never silently read as absence.
func newestHistoryMatch(pool, slug, sid string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(pool, slug, sid+"*.jsonl"))
	if err != nil {
		return "", fmt.Errorf("scan %s: %w", pool, err)
	}
	newest := ""
	var newestModTime time.Time
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return "", fmt.Errorf("stat %s: %w", match, statErr)
		}
		if newest == "" || info.ModTime().After(newestModTime) {
			newest = match
			newestModTime = info.ModTime()
		}
	}
	return newest, nil
}

// ReadHistory returns the last count user/assistant turns of the transcript at
// path. A Codex rollout is read through its own parser; any other file is a
// Claude transcript read by readClaudeHistory.
func ReadHistory(path string, count int) ([]HistoryMessage, error) {
	read := readClaudeHistory
	if codexmeta.IsRolloutName(filepath.Base(path)) {
		read = readCodexHistory
	}
	messages, err := read(path)
	if err != nil {
		return nil, err
	}
	if len(messages) > count {
		messages = messages[len(messages)-count:]
	}
	return messages, nil
}

// readCodexHistory keeps a rollout's user and assistant entries as From reads
// them: harness instructions and tool calls are already left out.
func readCodexHistory(path string) ([]HistoryMessage, error) {
	entries, _, err := From(context.Background(), path, string(pfmengine.Codex), 0)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var messages []HistoryMessage
	for _, entry := range entries {
		if entry.Role != RoleUser && entry.Role != RoleAssistant {
			continue
		}
		timestamp := entry.Timestamp
		if timestamp == "" {
			timestamp = "?"
		}
		messages = append(messages, HistoryMessage{Timestamp: timestamp, Role: entry.Role, Text: entry.Text})
	}
	return messages, nil
}

// readClaudeHistory is the native port of history.sh's jq pipeline: tail
// generously, keep only user/assistant records with non-empty rendered text,
// drop synthetic reminder and caveat preambles. The file is read whole, so
// every line of its tail is complete; a row that is not JSON stays in the list
// as an "unparsed" marker.
func readClaudeHistory(path string) (messages []HistoryMessage, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close %s: %w", path, err))
		}
	}()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	lines := tailLines(string(content), 800)
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := DecodeTolerant([]byte(line), &record); err != nil {
			// One bad row is not fatal and not silent: it costs itself, a marker stands in.
			messages = append(messages, HistoryMessage{
				Timestamp: "?",
				Role:      historyUnparsedRole,
				Text:      fmt.Sprintf("row skipped: not JSON (%d bytes)", len(line)),
			})
			continue
		}
		if record.Type != RoleUser && record.Type != RoleAssistant {
			continue
		}
		text := historyMessageText(record.Message.Content)
		if text == "" || isHistoryPreamble(text) {
			continue
		}
		timestamp := record.Timestamp
		if timestamp == "" {
			timestamp = "?"
		}
		messages = append(messages, HistoryMessage{Timestamp: timestamp, Role: record.Type, Text: text})
	}
	return messages, nil
}

// historyMessageText extracts message.content the way history.sh's jq does:
// a string is itself, less its leading reminders; an array joins the .text of
// every type=="text" element that is no reminder or caveat with "\n"; anything
// else renders as "".
func historyMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return stripLeadingReminders(asString)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == "text" && !isHistoryPreamble(part.Text) {
				texts = append(texts, part.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

// tailLines reproduces `tail -n count`: the last count newline-delimited
// lines, tolerant of a missing trailing newline and of fewer lines than
// count.
func tailLines(content string, count int) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		// A file ending in a newline splits into one trailing empty element
		// that is the terminator, not a line.
		lines = lines[:len(lines)-1]
	}
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return lines
}

// isHistoryPreamble is a harness-written reminder or caveat, no turn of the
// person's.
func isHistoryPreamble(text string) bool {
	return strings.HasPrefix(text, "<system-reminder") || strings.HasPrefix(text, "Caveat: The messages below")
}
