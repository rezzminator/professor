package codexappendix

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	historyLimit = 16 << 20
	recordLimit  = 32768
)

type record struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}
type message struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// presentInHistory reconstructs the ordinary local active window, using a
// replacement checkpoint when older bytes fall outside the fixed read budget.
// Native-only replay cases (lineage, rollback, legacy compaction) are explicitly
// unknown, rather than reimplementing a second, subtly different history engine.
func presentInHistory(path *string, body string) (present, older bool, returnErr error) {
	if path == nil || *path == "" {
		return false, false, fmt.Errorf("native hook supplied no local transcript")
	}
	f, err := os.Open(*path)
	if err != nil {
		return false, false, fmt.Errorf("open transcript: %w", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript: %w", err))
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return false, false, err
	}
	if !info.Mode().IsRegular() {
		return false, false, fmt.Errorf("transcript is not a regular file")
	}
	offset := max(int64(0), info.Size()-historyLimit)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return false, false, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, historyLimit+1))
	if err != nil {
		return false, false, err
	}
	if len(raw) > historyLimit {
		return false, false, fmt.Errorf("transcript grew beyond the 16 MiB read budget")
	}
	if offset > 0 {
		at := bytes.IndexByte(raw, '\n')
		if at < 0 {
			return false, false, fmt.Errorf("no complete record within read budget")
		}
		raw = raw[at+1:]
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return false, false, fmt.Errorf("transcript is empty or has an incomplete final record")
	}
	lines := bytes.Split(bytes.TrimSuffix(raw, []byte{'\n'}), []byte{'\n'})
	if len(lines) > recordLimit {
		return false, false, fmt.Errorf("transcript exceeds the %d record budget", recordLimit)
	}

	older = false
	inspect := func(m message) bool {
		if m.Type != "message" || m.Role != "developer" {
			return false
		}
		for _, c := range m.Content {
			if c.Text == body {
				return true
			}
			if strings.HasPrefix(c.Text, marker+"\n\n") {
				older = true
			}
		}
		return false
	}
	for i := len(lines) - 1; i >= 0; i-- {
		var r record
		if err := json.Unmarshal(lines[i], &r); err != nil {
			return false, older, fmt.Errorf("malformed transcript record: %w", err)
		}
		switch r.Type {
		case "response_item":
			var m message
			if err := json.Unmarshal(r.Payload, &m); err != nil {
				return false, older, err
			}
			if inspect(m) {
				return true, older, nil
			}
		case "compacted":
			var compact struct {
				History *[]message `json:"replacement_history"`
			}
			if err := json.Unmarshal(r.Payload, &compact); err != nil {
				return false, older, err
			}
			if compact.History == nil {
				return false, older, fmt.Errorf("legacy compaction has no replacement checkpoint")
			}
			found := false
			for _, m := range *compact.History {
				if inspect(m) {
					found = true
				}
			}
			return found, older, nil
		case "event_msg":
			var event struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(r.Payload, &event); err != nil {
				return false, older, err
			}
			if event.Type == "thread_rolled_back" {
				return false, older, fmt.Errorf("rollback requires native history replay")
			}
		case "session_meta":
			if offset == 0 && i == 0 {
				var meta struct {
					HistoryBase json.RawMessage `json:"history_base"`
				}
				if err := json.Unmarshal(r.Payload, &meta); err != nil {
					return false, older, err
				}
				if len(meta.HistoryBase) == 0 || string(meta.HistoryBase) == "null" {
					return false, older, nil
				}
				return false, older, fmt.Errorf("inherited history is stored in an ancestor rollout")
			}
		}
	}
	return false, older, fmt.Errorf("no active-history checkpoint within the read budget")
}
