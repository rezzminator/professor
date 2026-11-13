package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// agentEndsFold is the agents a transcript's task notifications end, each at
// the newest timestamp of a line ending it: every <task-id>{id}</task-id>
// followed, before the next task id, by a <status>completed|failed|killed</status>.
// Claude Code writes the notification into several entry kinds (queue
// operations, attachments, user messages, request copies without a timestamp),
// so the raw line is searched, not one decoded field; a line without a
// timestamp ends its agent at the zero time, and never moves a known end back.
type agentEndsFold struct {
	Ended map[string]time.Time `json:"ended"`
}

var (
	taskIDOpen  = []byte("<task-id>")
	taskIDClose = []byte("</task-id>")
	statusOpen  = []byte("<status>")
	statusClose = []byte("</status>")
)

func (fold *agentEndsFold) reset() { fold.Ended = map[string]time.Time{} }

func (fold *agentEndsFold) loaded() error {
	if fold.Ended == nil {
		return errors.New("agent-ends fold without its ended set")
	}
	return nil
}

func (fold *agentEndsFold) line(line []byte) {
	whole := line
	var stamp *time.Time
	at := func() time.Time {
		if stamp == nil {
			var entry struct {
				Timestamp time.Time `json:"timestamp"`
			}
			_ = json.Unmarshal(whole, &entry) // a line that does not decode ends at the zero time
			stamp = &entry.Timestamp
		}
		return *stamp
	}
	for {
		start := bytes.Index(line, taskIDOpen)
		if start < 0 {
			return
		}
		line = line[start+len(taskIDOpen):]
		end := bytes.Index(line, taskIDClose)
		if end < 0 {
			return
		}
		id := string(line[:end])
		line = line[end+len(taskIDClose):]
		scope := line
		if next := bytes.Index(scope, taskIDOpen); next >= 0 {
			scope = scope[:next]
		}
		open := bytes.Index(scope, statusOpen)
		if open < 0 {
			continue
		}
		status := scope[open+len(statusOpen):]
		if closing := bytes.Index(status, statusClose); closing >= 0 {
			switch string(status[:closing]) {
			case "completed", "failed", "killed":
				if known, seen := fold.Ended[id]; !seen || at().After(known) {
					fold.Ended[id] = at()
				}
			}
		}
	}
}
