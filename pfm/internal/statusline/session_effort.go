package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/nudge"
)

// sessionEffortRecord is the effort the main statusline last showed for a
// session, with the model it was resolved for. Claude Code 2.1.281 runs a
// sub-agent whose definition names no effort at its parent's live session
// effort (the agent context's effort is its own effort layer, else
// Il(parent app state, the sub-agent's model)), and its subagentStatusLine
// row payload then carries no effort (task.effort is the definition's), so
// the agent-panel row reads this record instead. Il resolves per model — a
// per-model settings table, max and xhigh clamped where unsupported — so the
// level is exact only for the model it was recorded with.
type sessionEffortRecord struct {
	Level string `json:"level"`
	Model string `json:"model"`
}

// RecordSession persists what the main statusline payload says about its
// session. The used-percentage feeds the compact-nudge hook, which reads
// Claude Code's own number instead of re-deriving a context window it cannot
// know (the transcript names the model, never the window); the effort feeds
// the agent-panel rows of the same session. Fail-open like the
// rest of the statusline: a record that cannot be written goes to warn and
// costs one reminder or one row's effort, never the status line.
func RecordSession(raw []byte, sidDir string, warn io.Writer) {
	var sample struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		ContextWindow  struct {
			UsedPercentage float64 `json:"used_percentage"`
		} `json:"context_window"`
		Model struct {
			ID string `json:"id"`
		} `json:"model"`
		Effort struct {
			Level string `json:"level"`
		} `json:"effort"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		return // Render reports malformed input itself
	}
	sessionID := strings.TrimSpace(sample.SessionID)
	if sessionID == "" && sample.TranscriptPath != "" {
		sessionID = strings.TrimSuffix(filepath.Base(sample.TranscriptPath), filepath.Ext(sample.TranscriptPath))
	}
	if sessionID == "" {
		return
	}
	if err := nudge.RecordContext(sidDir, sessionID, int(sample.ContextWindow.UsedPercentage)); err != nil {
		fmt.Fprintf(warn, "pfm statusline: record context sample (fail-open): %v\n", err)
	}
	record := sessionEffortRecord{
		Level: strings.TrimSpace(sample.Effort.Level),
		Model: strings.TrimSpace(sample.Model.ID),
	}
	if err := writeSessionEffort(sidDir, sessionID, record); err != nil {
		fmt.Fprintf(warn, "pfm statusline: record session effort (fail-open): %v\n", err)
	}
}

// writeSessionEffort replaces the session's record whole, so a row reading it
// mid-write sees the old record or the new one; an empty level is recorded
// too, so a session that dropped its effort never keeps a stale one.
func writeSessionEffort(sidDir, sessionID string, record sessionEffortRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode session effort: %w", err)
	}
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		return fmt.Errorf("create session effort directory %s: %w", sidDir, err)
	}
	path := sessionEffortPath(sidDir, sessionID)
	if err := atomicfile.Write(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func sessionEffortPath(sidDir, sessionID string) string {
	return filepath.Join(sidDir, "statusline-effort-"+sessionID)
}

// inheritedEffort reads the effort recorded for sessionID: no directory, no
// session or no record yet is the zero record and no error; a record that
// exists but cannot be read or parsed is an error naming its path.
func inheritedEffort(sidDir, sessionID string) (sessionEffortRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sidDir == "" || sessionID == "" {
		return sessionEffortRecord{}, nil
	}
	path := sessionEffortPath(sidDir, sessionID)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return sessionEffortRecord{}, nil
	}
	if err != nil {
		return sessionEffortRecord{}, fmt.Errorf("read %s: %w", path, err)
	}
	var record sessionEffortRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return sessionEffortRecord{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return record, nil
}
