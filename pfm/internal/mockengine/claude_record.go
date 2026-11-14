package mockengine

import (
	"encoding/json"
	"fmt"
	"time"
	"uuid"
)

// Transcript records, in the keys internal/transcript/transcript.go:49-75,
// meta.go:47-84 and internal/index/claude.go:13-25 read.
type claudeRecord struct {
	Type             string           `json:"type"`
	UUID             string           `json:"uuid,omitempty"`
	SessionID        string           `json:"sessionId,omitempty"`
	Timestamp        string           `json:"timestamp,omitempty"`
	CWD              string           `json:"cwd,omitempty"`
	IsSidechain      bool             `json:"isSidechain"`
	IsMeta           bool             `json:"isMeta,omitempty"`
	IsCompactSummary bool             `json:"isCompactSummary,omitempty"`
	AgentID          string           `json:"agentId,omitempty"`
	Entrypoint       string           `json:"entrypoint,omitempty"`
	PromptSource     string           `json:"promptSource,omitempty"`
	CustomTitle      string           `json:"customTitle,omitempty"`
	AITitle          string           `json:"aiTitle,omitempty"`
	Subtype          string           `json:"subtype,omitempty"`
	CompactMetadata  *compactMetadata `json:"compactMetadata,omitempty"`
	Message          *claudeMessage   `json:"message,omitempty"`
	// IsAPIErrorMessage and Error mark Claude's synthetic reply to a turn the
	// model server refused; Error is its kind.
	IsAPIErrorMessage bool   `json:"isApiErrorMessage,omitempty"`
	Error             string `json:"error,omitempty"`
}

type compactMetadata struct {
	Trigger    string `json:"trigger"`
	PreTokens  int64  `json:"preTokens"`
	PostTokens int64  `json:"postTokens"`
}

type claudeMessage struct {
	Role    string       `json:"role"`
	Model   string       `json:"model,omitempty"`
	Content any          `json:"content"`
	Usage   *claudeUsage `json:"usage,omitempty"`
}

type claudeUsage struct {
	Input         int64 `json:"input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	Output        int64 `json:"output_tokens"`
}

type contentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

func (session *claudeSession) userRecord(text string, meta bool) claudeRecord {
	return claudeRecord{
		Type:         roleUser,
		UUID:         uuid.NewV4().String(),
		SessionID:    session.sessionID,
		Timestamp:    stamp(time.Now()),
		CWD:          session.proc.cwd,
		IsMeta:       meta,
		Entrypoint:   "cli",
		PromptSource: "typed",
		Message:      &claudeMessage{Role: roleUser, Content: text},
	}
}

func (session *claudeSession) assistantRecord(parts []contentPart, usage Tokens, sidechain bool) claudeRecord {
	return claudeRecord{
		Type: roleAssistant, UUID: uuid.NewV4().String(), SessionID: session.sessionID, Timestamp: stamp(time.Now()),
		CWD: session.proc.cwd, IsSidechain: sidechain,
		Message: &claudeMessage{
			Role:    roleAssistant,
			Model:   session.model,
			Content: parts,
			Usage: &claudeUsage{
				Input:         usage.Input,
				CacheCreation: usage.CacheCreation,
				CacheRead:     usage.CacheRead,
				Output:        usage.Output,
			},
		},
	}
}

func (session *claudeSession) write(record claudeRecord) error {
	if session.proc.script.NoTranscript {
		return nil
	}
	if session.fork != nil {
		session.fork.held = append(session.fork.held, record)
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode transcript record: %w", err)
	}
	return appendLine(session.transcript, encoded)
}
