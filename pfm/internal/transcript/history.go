package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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

// ReadHistory is the native port of history.sh's jq pipeline: tail
// generously, drop the (possibly partial) first line, keep only user/
// assistant records with non-empty rendered text, drop synthetic reminder and
// caveat preambles, then take the last count survivors.
func ReadHistory(path string, count int) (messages []HistoryMessage, returnErr error) {
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
	if len(lines) > 0 {
		lines = lines[1:]
	}
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
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			// Match history.sh: a malformed transcript row is skipped, not fatal.
			continue
		}
		if record.Type != RoleUser && record.Type != RoleAssistant {
			continue
		}
		text := historyMessageText(record.Message.Content)
		if text == "" || strings.HasPrefix(text, "<system-reminder") ||
			strings.HasPrefix(text, "Caveat: The messages below") {
			continue
		}
		timestamp := record.Timestamp
		if timestamp == "" {
			timestamp = "?"
		}
		messages = append(messages, HistoryMessage{Timestamp: timestamp, Role: record.Type, Text: text})
	}
	if len(messages) > count {
		messages = messages[len(messages)-count:]
	}
	return messages, nil
}

// historyMessageText extracts message.content the way history.sh's jq does:
// a string is itself; an array joins the .text of every type=="text" element
// with "\n"; anything else renders as "".
func historyMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == "text" {
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
