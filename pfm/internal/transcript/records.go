package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
)

// ResponseKey names the API response an assistant record belongs to: one
// response is one key, however many lines repeat its usage. The message id,
// else the request id, else the record's uuid, else at (where the record sits,
// such as file:line); each form is namespaced, so a request id never equals a
// message id. "" when every form is empty.
func ResponseKey(messageID, requestID, uuid, at string) string {
	switch {
	case messageID != "":
		return "message:" + messageID
	case requestID != "":
		return "request:" + requestID
	case uuid != "":
		return "uuid:" + uuid
	case at != "":
		return "at:" + at
	}
	return ""
}

// CodexTokenUsage is one usage object of a Codex token_count event.
// CachedInputTokens is a part of InputTokens, ReasoningOutputTokens a part of
// OutputTokens.
type CodexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// Total is the usage's total_tokens; 0 for a usage the event did not carry.
func (usage *CodexTokenUsage) Total() int64 {
	if usage == nil {
		return 0
	}
	return usage.TotalTokens
}

// CodexTokenInfo is a token_count event's info: the thread's cumulative usage
// (it resets on resume and compaction), the last request's, and the model's
// window. A usage the event does not carry is nil.
type CodexTokenInfo struct {
	TotalTokenUsage    *CodexTokenUsage `json:"total_token_usage"`
	LastTokenUsage     *CodexTokenUsage `json:"last_token_usage"`
	ModelContextWindow int64            `json:"model_context_window"`
}

// ClaudeRecord is one Claude transcript record — the one decoder of a record
// line that tokens and the digest share. Every member is kept raw: a record's
// members vary by type, and a member of an unexpected shape must read as
// absent to one reader and as written to another, never fail the line.
type ClaudeRecord struct {
	Type             json.RawMessage `json:"type"`
	Subtype          json.RawMessage `json:"subtype"`
	Timestamp        json.RawMessage `json:"timestamp"`
	CWD              json.RawMessage `json:"cwd"`
	CustomTitle      json.RawMessage `json:"customTitle"`
	AgentName        json.RawMessage `json:"agentName"`
	AITitle          json.RawMessage `json:"aiTitle"`
	TotalCostUSD     json.RawMessage `json:"totalCostUSD"`
	StartTime        json.RawMessage `json:"startTime"`
	TotalDuration    json.RawMessage `json:"totalDuration"`
	Message          json.RawMessage `json:"message"`
	RequestID        json.RawMessage `json:"requestId"`
	UUID             json.RawMessage `json:"uuid"`
	SessionID        json.RawMessage `json:"sessionId"`
	NamedSession     json.RawMessage `json:"session_id"`
	AgentID          json.RawMessage `json:"agentId"`
	ForkedFrom       json.RawMessage `json:"forkedFrom"`
	Effort           json.RawMessage `json:"effort"`
	PerTurnEffort    json.RawMessage `json:"perTurnEffort"`
	Origin           json.RawMessage `json:"origin"`
	IsCompactSummary json.RawMessage `json:"isCompactSummary"`
	IsMeta           json.RawMessage `json:"isMeta"`
	Attachment       json.RawMessage `json:"attachment"`
}

// ClaudeMessage is a record's message object. Usage stays raw until a reader
// asks for it, so a malformed usage block never hides the content.
type ClaudeMessage struct {
	ID         json.RawMessage `json:"id"`
	Model      json.RawMessage `json:"model"`
	Usage      json.RawMessage `json:"usage"`
	Content    json.RawMessage `json:"content"`
	StopReason json.RawMessage `json:"stop_reason"`
}

// DecodeClaudeRecord decodes one line; ok is false for a line that is no JSON
// object.
func DecodeClaudeRecord(line []byte) (record ClaudeRecord, ok bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &record) != nil {
		return ClaudeRecord{}, false
	}
	return record, true
}

// Kind is the record's type when it is a string. ok is false for a type of
// any other form; an absent or null type is "", true.
func (record *ClaudeRecord) Kind() (kind string, ok bool) {
	return stringMember(record.Type)
}

// DecodeMessage decodes the record's message object; nil when it is none.
func (record *ClaudeRecord) DecodeMessage() *ClaudeMessage {
	raw := bytes.TrimSpace(record.Message)
	var message ClaudeMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &message) != nil {
		return nil
	}
	return &message
}

// UsageBlock decodes the message's usage; nil, nil when it carries none.
func (message *ClaudeMessage) UsageBlock() (*pricing.UsageBlock, error) {
	raw := bytes.TrimSpace(message.Usage)
	if pyvalue.IsNull(raw) {
		return nil, nil
	}
	var usage pricing.UsageBlock
	if err := json.Unmarshal(raw, &usage); err != nil {
		return nil, fmt.Errorf("decode message usage: %w", err)
	}
	return &usage, nil
}

// CodexRecord is one Codex rollout record, decoded loosely: every member a
// reader tests is kept raw, the payload until a reader asks for it.
type CodexRecord struct {
	Type      json.RawMessage `json:"type"`
	Timestamp json.RawMessage `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// CodexPayload is a rollout record's payload: one type for every payload
// shape the readers share (messages, tool calls and their outputs, turn
// context, token counts, turn ends, completed items).
type CodexPayload struct {
	Type              json.RawMessage `json:"type"`
	Timestamp         json.RawMessage `json:"timestamp"`
	Role              json.RawMessage `json:"role"`
	Content           json.RawMessage `json:"content"`
	Phase             json.RawMessage `json:"phase"`
	Name              json.RawMessage `json:"name"`
	CallID            json.RawMessage `json:"call_id"`
	Input             json.RawMessage `json:"input"`
	Arguments         json.RawMessage `json:"arguments"`
	Output            json.RawMessage `json:"output"`
	Item              json.RawMessage `json:"item"`
	LastAgentMessage  json.RawMessage `json:"last_agent_message"`
	Reason            json.RawMessage `json:"reason"`
	Message           json.RawMessage `json:"message"`
	Model             json.RawMessage `json:"model"`
	Effort            json.RawMessage `json:"effort"`
	CollaborationMode json.RawMessage `json:"collaboration_mode"`
	Info              json.RawMessage `json:"info"`
}

// DecodeCodexRecord decodes one line; ok is false for a line that is no JSON
// object.
func DecodeCodexRecord(line []byte) (record CodexRecord, ok bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &record) != nil {
		return CodexRecord{}, false
	}
	return record, true
}

// Kind is the record's type when it is a string; ok is false for a type of
// any other form.
func (record *CodexRecord) Kind() (kind string, ok bool) {
	return stringMember(record.Type)
}

// DecodePayload decodes the payload object: nil, true when the record carries
// none (absent or null); nil, false when it carries one of another form.
func (record *CodexRecord) DecodePayload() (*CodexPayload, bool) {
	raw := bytes.TrimSpace(record.Payload)
	if pyvalue.IsNull(raw) {
		return nil, true
	}
	var payload CodexPayload
	if raw[0] != '{' || json.Unmarshal(raw, &payload) != nil {
		return nil, false
	}
	return &payload, true
}

// Kind is the payload's type when it is a string. ok is false for a type of
// any other form; an absent or null type is "", true.
func (payload *CodexPayload) Kind() (kind string, ok bool) {
	return stringMember(payload.Type)
}

// TokenInfo decodes a token_count payload's info; nil, nil when it carries
// none.
func (payload *CodexPayload) TokenInfo() (*CodexTokenInfo, error) {
	raw := bytes.TrimSpace(payload.Info)
	if pyvalue.IsNull(raw) {
		return nil, nil
	}
	var info CodexTokenInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("decode token_count info: %w", err)
	}
	return &info, nil
}

// stringMember is a member's string value; ok is false for a present value of
// another form (an absent or null member is "", true).
func stringMember(raw json.RawMessage) (string, bool) {
	if pyvalue.IsNull(raw) {
		return "", true
	}
	return pyvalue.String(raw)
}

// NotARequest reports an assistant record's usage as no model request: a
// harness-made <synthetic> message, or a usage block that bills nothing and
// carries no advisor.
func NotARequest(model string, block *pricing.UsageBlock) bool {
	usage, _ := block.Billed()
	return model == "<synthetic>" || usage == (pricing.ClaudeUsage{}) && len(block.Advisors()) == 0
}
