package codexmeta

import (
	"encoding/json"
	"fmt"
	"time"
)

// SessionIndexFile is Codex's own name ledger inside a Codex home: one JSON
// line per rename, appended by the TUI's thread/name/set.
const SessionIndexFile = "session_index.jsonl"

// SessionIndexEntry is one session_index.jsonl line.
type SessionIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	// UpdatedAt is the rename time Codex 0.147 began stamping on every entry.
	// Older entries carry none.
	UpdatedAt string `json:"updated_at"`
}

// DecodeSessionIndexLine parses one line. An entry without an id is refused:
// it names no thread.
func DecodeSessionIndexLine(line []byte) (SessionIndexEntry, error) {
	var entry SessionIndexEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return SessionIndexEntry{}, fmt.Errorf("decode Codex session index line: %w", err)
	}
	if entry.ID == "" {
		return SessionIndexEntry{}, fmt.Errorf("session index line for Codex has no thread id")
	}
	return entry, nil
}

// RenamedAt is the entry's rename time; ok is false when Codex stamped none or
// stamped something unreadable, so "no time" never reads as the epoch.
func (entry SessionIndexEntry) RenamedAt() (renamed time.Time, ok bool) {
	if entry.UpdatedAt == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, entry.UpdatedAt)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
