package transcript

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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
// carries no advisor. It is the one rule tokens and RequestCount share.
func NotARequest(model string, block *pricing.UsageBlock) bool {
	usage, _ := block.Billed()
	return model == "<synthetic>" || usage == (pricing.ClaudeUsage{}) && len(block.Advisors()) == 0
}

// assistantMarker is a byte every assistant record line carries, so a line
// without it skips the decode.
var assistantMarker = []byte(`"` + RoleAssistant + `"`)

// RequestCount is the number of model requests path's Claude transcript
// records: the distinct ResponseKey of every assistant record whose usage is a
// request (NotARequest). A compaction neither resets nor adds to it, so it
// spans every compaction one agent's file holds; a line that is no record, a
// malformed usage block and a half-written tail count nothing.
func RequestCount(path string) (int, error) {
	var tally RequestTally
	count, _, err := tally.Resume(path)
	return count, err
}

// tallyHeadBytes is how much of a transcript's start a RequestTally hashes to
// tell the file it read from a file rewritten under the same path.
const tallyHeadBytes = 4096

// RequestTally is RequestCount's fold, kept between reads of one append-only
// transcript: Offset is the end of the last complete line read, Lines the
// complete lines before it, Head the hex sha256 of the file's first
// min(Offset, tallyHeadBytes) bytes, Keys the requests' ResponseKeys.
type RequestTally struct {
	Offset int64           `json:"offset"`
	Lines  int             `json:"lines"`
	Head   string          `json:"head"`
	Keys   map[string]bool `json:"keys"`
}

// Resume counts path's model requests as RequestCount does, reading only the
// bytes after the tally's Offset; a file shorter than Offset, or whose head no
// longer hashes to Head, is counted from its start, as is a tally whose Offset
// or Lines is negative (damaged outside Resume). Complete lines fold into
// the tally; a half-written tail counts toward this count alone. bytesRead is
// every byte read past the head check.
func (t *RequestTally) Resume(path string) (count int, bytesRead int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("count model requests: %w", &OpenError{Path: path, Err: err})
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %q: %w", path, closeErr))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("count model requests: stat %q: %w", path, err)
	}
	if t.Offset > 0 {
		head, err := tallyHead(file, t.Offset)
		if err != nil {
			return 0, 0, fmt.Errorf("count model requests: %w", err)
		}
		if info.Size() < t.Offset || head != t.Head {
			*t = RequestTally{}
		}
	}
	if t.Offset <= 0 || t.Lines < 0 {
		*t = RequestTally{}
		noteWholeRead(path)
	}
	if t.Keys == nil {
		t.Keys = map[string]bool{}
	}
	if _, err := file.Seek(t.Offset, io.SeekStart); err != nil {
		return 0, 0, fmt.Errorf("count model requests: seek %q to %d: %w", path, t.Offset, err)
	}
	tail := ""
	parsedOffset, bytesRead, err := ScanLines(file, t.Offset, func(line []byte, complete bool) {
		number := t.Lines + 1
		if complete {
			t.Lines++
		}
		key := requestKey(line, number)
		switch {
		case key == "":
		case complete:
			t.Keys[key] = true
		default:
			tail = key
		}
	})
	if err != nil {
		return 0, bytesRead, fmt.Errorf("count model requests: read %q: %w", path, err)
	}
	if t.Offset < tallyHeadBytes && parsedOffset != t.Offset {
		if t.Head, err = tallyHead(file, parsedOffset); err != nil {
			return 0, bytesRead, fmt.Errorf("count model requests: %w", err)
		}
	}
	t.Offset = parsedOffset
	count = len(t.Keys)
	if tail != "" && !t.Keys[tail] {
		count++
	}
	return count, bytesRead, nil
}

// tallyHead is the hex sha256 of file's first min(offset, tallyHeadBytes) bytes.
func tallyHead(file *os.File, offset int64) (string, error) {
	sum := sha256.New()
	if _, err := io.Copy(sum, io.NewSectionReader(file, 0, min(offset, tallyHeadBytes))); err != nil {
		return "", fmt.Errorf("hash the head of %q: %w", file.Name(), err)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// requestKey is the ResponseKey of the model request a transcript line
// records, line number holding the place of a record with no id; "" for a
// line that records none.
func requestKey(line []byte, number int) string {
	if !bytes.Contains(line, assistantMarker) {
		return ""
	}
	record, ok := DecodeClaudeRecord(line)
	if !ok {
		return ""
	}
	if kind, _ := record.Kind(); kind != RoleAssistant {
		return ""
	}
	message := record.DecodeMessage()
	if message == nil {
		return ""
	}
	block, err := message.UsageBlock()
	if err != nil || block == nil {
		return ""
	}
	model, _ := stringMember(message.Model)
	if NotARequest(model, block) {
		return ""
	}
	messageID, _ := stringMember(message.ID)
	requestID, _ := stringMember(record.RequestID)
	uuid, _ := stringMember(record.UUID)
	return ResponseKey(messageID, requestID, uuid, "line:"+strconv.Itoa(number))
}

// CallCapRole names the role whose cap an agent type's name carries:
// "foreman", "lander", or "" for every other agent. CallCap and the call-cap
// hook's stop read this one classification.
func CallCapRole(agentType string) string {
	name := strings.ToLower(agentType)
	switch {
	case strings.Contains(name, "foreman"):
		return "foreman"
	case strings.Contains(name, "lander"):
		return "lander"
	}
	return ""
}

// CallCap is the cap on the model requests (RequestCount) an agent type's
// name sets: a foreman 250, a lander 200, everything else 150. The flight
// report flags a run past it, and the call-cap hook stops an agent at it.
func CallCap(agentType string) int {
	switch CallCapRole(agentType) {
	case "foreman":
		return 250
	case "lander":
		return 200
	}
	return 150
}
