package reap

import (
	"bytes"
	"encoding/json"
	"os"
	"time"
)

// activityRecord is the shape one transcript or rollout line's own timestamp
// can take. Two other signals were measured against the live fleet and both
// lied: tmux's own #{window_activity} reports minutes-old for a chat
// abandoned 17 days ago, because the TUI redraws its statusline forever, and
// the transcript FILE's mtime gets rewritten every 30-60 minutes by
// bookkeeping records (custom-title, last-prompt, bridge-session, ...) that
// carry no timestamp of their own and no conversational activity behind
// them. The only signal that held up is the timestamp INSIDE the last
// record that actually has one — read here.
type activityRecord struct {
	Timestamp string          `json:"timestamp"`
	TS        json.RawMessage `json:"ts"`
}

// lastRecordTime walks a JSONL transcript or rollout backward from its last
// line, skipping any line that fails to parse (a truncated tail from a chat
// still mid-write) and any record that carries neither field, and returns
// the newest parseable record's own timestamp. found is false when the file
// could not be read at all, or when nothing in it ever parsed — that is
// UNKNOWN, and UNKNOWN is never a measurement of idleness.
func lastRecordTime(path string) (stamp time.Time, found bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	lines := bytes.Split(content, []byte("\n"))
	for index := len(lines) - 1; index >= 0; index-- {
		line := bytes.TrimSpace(lines[index])
		if len(line) == 0 {
			continue
		}
		var record activityRecord
		if err := json.Unmarshal(line, &record); err != nil {
			// A corrupt or truncated tail line proves nothing about the
			// record before it — keep walking backward rather than giving up.
			continue
		}
		if parsed, ok := parseTimestampField(record.Timestamp); ok {
			return parsed, true
		}
		// An absent or unparseable "timestamp" is not a verdict on the record:
		// its "ts" field is still tried.
		if parsed, ok := parseTSField(record.TS); ok {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// parseTimestampField reads the ISO-8601 shape ("timestamp": "...").
func parseTimestampField(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// parseTSField reads the "ts" shape, which measurement on the live fleet
// showed in two forms: an ISO-8601 string, same as "timestamp", or a bare
// epoch number — milliseconds when it is too large to be seconds (anything
// past 1e11 is already the year 5138 in seconds, so it can only be ms).
func parseTSField(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return parseTimestampField(asString)
	}
	var asNumber float64
	if err := json.Unmarshal(raw, &asNumber); err != nil {
		return time.Time{}, false
	}
	if asNumber > 1e11 {
		return time.UnixMilli(int64(asNumber)), true
	}
	return time.Unix(int64(asNumber), 0), true
}

// socketActivity is the newest lastRecordTime across every path this
// socket's chat writes — a /chat:branch split can put two transcripts on one
// socket, and the socket is idle only once BOTH are. found is false the
// moment there is nothing readable to measure, so one unreadable path can
// never be papered over by a readable one that happens to be old.
func socketActivity(paths []string) (stamp time.Time, found bool) {
	for _, path := range paths {
		recordStamp, ok := lastRecordTime(path)
		if !ok {
			return time.Time{}, false
		}
		if !found || recordStamp.After(stamp) {
			stamp = recordStamp
			found = true
		}
	}
	return stamp, found
}
