// Package transcript parses what a chat actually said, from the engine's own
// transcript file — a Claude registry jsonl or a Codex rollout jsonl.
//
// It is the single parser in this tree: reading a chat off its tmux pane loses
// everything that scrolled, blurs a dead chat into a quiet one, and breaks the
// day an engine repaints. The file is the record.
package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
)

// Roles an Entry can carry.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSummary   = "summary"
)

// Condensed line caps, the established recipe for feeding a transcript to
// another model cheaply: a tool call is identified, not reproduced; a message
// is recognizable, not complete.
const (
	ToolInputCap = 160
	TextCap      = 220
)

// Entry is one thing that happened in a chat.
type Entry struct {
	Role      string `json:"role"`
	Text      string `json:"text,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Input     string `json:"input,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	// Error is the kind of the error a turn ended on — set only on the
	// assistant entry that stands for that error (Codex's task_complete with an
	// error, Claude's synthetic API-error message) — so a reader can tell a
	// turn the model server stopped from one the assistant finished.
	Error string `json:"error,omitempty"`
}

type record struct {
	Type             string          `json:"type"`
	Timestamp        string          `json:"timestamp"`
	Summary          string          `json:"summary"`
	IsSidechain      bool            `json:"isSidechain"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Message          messageRecord   `json:"message"`
	Payload          payloadRecord   `json:"payload"`
	Content          json.RawMessage `json:"content"`
	Role             string          `json:"role"`
	// IsAPIErrorMessage and Error mark Claude's synthetic assistant message
	// for a turn that ended on an API error; Error is its kind
	// ("server_error", "rate_limit", ...). Raw, so a record whose error is not
	// a string still parses.
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	Error             json.RawMessage `json:"error"`
}

type messageRecord struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type payloadRecord struct {
	Type      string          `json:"type"`
	Message   json.RawMessage `json:"message"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp string          `json:"timestamp"`
	Name      string          `json:"name"`
	Input     string          `json:"input"`
	Arguments string          `json:"arguments"`
	// Reason is a Codex turn_aborted's cause ("interrupted").
	Reason string `json:"reason"`
	// Error is a Codex task_complete's turn error, null when the turn
	// finished. Raw, so a payload whose error has another shape still parses.
	Error json.RawMessage `json:"error"`
}

// codexTurnError is a Codex task_complete's error: the message its TUI shows
// and the kind (codex_error_info), a bare string or a one-key object.
type codexTurnError struct {
	Message string          `json:"message"`
	Info    json.RawMessage `json:"codex_error_info"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Parse reads one transcript line. The second result is false for every line
// that carries nothing a reader would want: sidechains, compaction summaries,
// token counts, and the harness-injected records naming.IsJunkPrompt names.
func Parse(line []byte, engine string) (Entry, bool) {
	var parsed record
	if err := json.Unmarshal(line, &parsed); err != nil || parsed.IsSidechain {
		return Entry{}, false
	}
	if engine == string(pfmengine.Codex) {
		return parseCodex(parsed)
	}
	return parseClaude(parsed)
}

func parseClaude(parsed record) (Entry, bool) {
	switch parsed.Type {
	case "summary":
		if parsed.Summary == "" {
			return Entry{}, false
		}
		return Entry{
			Role:      RoleSummary,
			Text:      parsed.Summary,
			Timestamp: parsed.Timestamp,
		}, true
	case "user", "assistant":
		if parsed.IsCompactSummary {
			return Entry{}, false
		}
		if tool, ok := toolCall(parsed.Message.Content); ok {
			tool.Timestamp = parsed.Timestamp
			return tool, true
		}
		text := Visible(parsed.Message.Content)
		if text == "" {
			return Entry{}, false
		}
		if parsed.Type == "user" && strings.HasPrefix(text, claudeInterruptMarker) {
			// Before the junk drop: the marker starts with "[Request", which
			// naming.IsJunkPrompt discards, and an interrupted turn must not
			// vanish from the record.
			return interruptedTurn("interrupted", parsed.Timestamp), true
		}
		if parsed.Type == "user" && naming.IsJunkPrompt(text) {
			return Entry{}, false
		}
		entry := Entry{
			Role:      parsed.Type,
			Text:      text,
			Timestamp: parsed.Timestamp,
		}
		if parsed.Type == RoleAssistant && parsed.IsAPIErrorMessage {
			entry.Error = turnErrorKind(parsed.Error)
		}
		return entry, true
	default:
		return Entry{}, false
	}
}

func parseCodex(parsed record) (Entry, bool) {
	timestamp := parsed.Timestamp
	if timestamp == "" {
		timestamp = parsed.Payload.Timestamp
	}
	switch parsed.Payload.Type {
	case "custom_tool_call":
		return Entry{
			Role:      RoleTool,
			Tool:      parsed.Payload.Name,
			Input:     parsed.Payload.Input,
			Timestamp: timestamp,
		}, parsed.Payload.Name != ""
	case "function_call":
		return Entry{
			Role:      RoleTool,
			Tool:      parsed.Payload.Name,
			Input:     parsed.Payload.Arguments,
			Timestamp: timestamp,
		}, parsed.Payload.Name != ""
	case "task_complete":
		return codexTurnEnd(parsed.Payload.Error, timestamp)
	case "turn_aborted":
		return interruptedTurn(parsed.Payload.Reason, timestamp), true
	}

	role := ""
	var content json.RawMessage
	switch parsed.Payload.Type {
	case "user_message":
		role = RoleUser
		content = parsed.Payload.Message
	// "agent_message" is not handled here: it is an event_msg record that
	// always pairs with a response_item message carrying the identical text
	// (internal/mockengine/codex.go's recordAssistant writes both; real
	// Codex rollouts do the same). internal/index/codex.go treats the
	// response_item as the one canonical record for a turn already paired
	// with an event — counting the event too reports the same reply twice.
	case "message":
		role = parsed.Payload.Role
		content = parsed.Payload.Content
	default:
		if parsed.Type == "user_message" {
			role = RoleUser
			content = parsed.Message.Content
			if len(content) == 0 {
				content = parsed.Content
			}
		}
	}
	// A Codex rollout also carries "developer" and "system" roles — the
	// permissions preamble and the harness instructions. They are not what the
	// chat said.
	if role != RoleUser && role != RoleAssistant {
		return Entry{}, false
	}
	text := Visible(content)
	if text == "" || (role == RoleUser && naming.IsJunkPrompt(text)) {
		return Entry{}, false
	}
	return Entry{Role: role, Text: text, Timestamp: timestamp}, true
}

// codexTurnEnd turns a task_complete into an entry only when the turn ended
// on an error: Codex then writes no assistant message, and the newest entry
// would otherwise be whatever the error cut short. The error stands in the
// assistant's place, as Claude's synthetic API-error message already does.
func codexTurnEnd(raw json.RawMessage, timestamp string) (Entry, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return Entry{}, false
	}
	var turnError codexTurnError
	if err := json.Unmarshal(raw, &turnError); err != nil {
		// Not the object shape: the turn still ended on an error, whose
		// text is the raw value itself.
		turnError.Message = strings.Trim(string(raw), `"`)
	}
	kind := turnErrorKind(turnError.Info)
	text := turnError.Message
	if text == "" {
		text = kind
	}
	return Entry{Role: RoleAssistant, Text: text, Error: kind, Timestamp: timestamp}, true
}

// claudeInterruptMarker starts the user record Claude Code writes when its
// human interrupts a turn ("[Request interrupted by user]", "... for tool use]").
const claudeInterruptMarker = "[Request interrupted by user"

// interruptedTurn is the entry for a turn its human aborted (Codex's
// turn_aborted, Claude's interrupt marker). The abort stands in the assistant's
// place exactly as codexTurnEnd's error does: the turn ended and the chat waits
// for its human, so the newest entry must not stay whatever the abort cut short.
func interruptedTurn(reason, timestamp string) Entry {
	if reason == "" {
		reason = "interrupted"
	}
	return Entry{Role: RoleAssistant, Text: "[turn aborted: " + reason + "]", Timestamp: timestamp}
}

// turnErrorKind names an error from its kind field: a bare string, the key of a
// one-key object (Codex's codex_error_info), or "unknown" when the record
// carries none — never empty, since an empty kind reads as no error.
func turnErrorKind(raw json.RawMessage) string {
	var kind string
	if err := json.Unmarshal(raw, &kind); err == nil && kind != "" {
		return kind
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil && len(object) == 1 {
		for key := range object {
			return key
		}
	}
	return "unknown"
}

// toolCall extracts the first tool_use block of a Claude message.
func toolCall(raw json.RawMessage) (Entry, bool) {
	blocks, ok := decodeBlocks(raw)
	if !ok {
		return Entry{}, false
	}
	for _, block := range blocks {
		if block.Type != "tool_use" || block.Name == "" {
			continue
		}
		return Entry{
			Role:  RoleTool,
			Tool:  block.Name,
			Input: strings.TrimSpace(string(block.Input)),
		}, true
	}
	return Entry{}, false
}

// Visible returns the human-readable text of a message body, dropping the
// system reminders the harness injects into user turns.
func Visible(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.HasPrefix(text, "<system-reminder>") {
			return ""
		}
		return text
	}
	blocks, ok := decodeBlocks(raw)
	if !ok {
		return ""
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text", "output_text":
			if block.Text != "" && !strings.HasPrefix(block.Text, "<system-reminder>") {
				texts = append(texts, block.Text)
			}
		}
	}
	return strings.Join(texts, "\n\n")
}

func decodeBlocks(raw json.RawMessage) ([]contentBlock, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false
	}
	return blocks, true
}

// Condensed renders one entry as a single line: T for a tool call, A for the
// assistant, E for the error a turn ended on, U for the human, S for a
// compaction summary.
func Condensed(entry Entry) string {
	switch entry.Role {
	case RoleTool:
		return "T " + entry.Tool + "|" + flatten(Truncate(entry.Input, ToolInputCap))
	case RoleAssistant:
		if entry.Error != "" {
			return "E " + entry.Error + ": " + flatten(Truncate(entry.Text, TextCap))
		}
		return "A " + flatten(Truncate(entry.Text, TextCap))
	case RoleUser:
		return "U " + flatten(Truncate(entry.Text, TextCap))
	case RoleSummary:
		return "S " + flatten(Truncate(entry.Text, TextCap))
	default:
		return ""
	}
}

func flatten(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// Truncate cuts to a byte budget without splitting a rune.
func Truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) && value != "" {
		value = value[:len(value)-1]
	}
	return value
}

// Tail returns the last lastN entries, each capped at maxBytes, and whether
// anything was left out.
func Tail(
	ctx context.Context,
	path, engine string,
	lastN, maxBytes int,
) (entries []Entry, truncated bool, returnErr error) {
	if lastN < 1 {
		return nil, false, fmt.Errorf("tail count must be positive, got %d", lastN)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %s: %w", path, err))
		}
	}()

	reader := bufio.NewReaderSize(file, 64<<10)
	ring := make([]Entry, 0, lastN)
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if entry, ok := Parse(line, engine); ok {
				total++
				if maxBytes > 0 {
					entry.Text = Truncate(entry.Text, maxBytes)
					entry.Input = Truncate(entry.Input, maxBytes)
				}
				if len(ring) == lastN {
					copy(ring, ring[1:])
					ring[len(ring)-1] = entry
				} else {
					ring = append(ring, entry)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, false, readErr
		}
	}
	return ring, total > len(ring), nil
}

// All returns every visible transcript entry in file order. It shares Parse
// with tail/status so save and excerpt loading cannot invent a second record
// interpretation.
func All(ctx context.Context, path, engine string) (entries []Entry, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %s: %w", path, err))
		}
	}()
	reader := bufio.NewReaderSize(file, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if entry, ok := Parse(line, engine); ok {
				entries = append(entries, entry)
			}
		}
		if readErr == io.EOF {
			return entries, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

// Last returns the newest entry with the given role.
func Last(entries []Entry, role string) (Entry, bool) {
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].Role == role {
			return entries[index], true
		}
	}
	return Entry{}, false
}

// LastExchange isolates the newest human turn and every visible record that
// followed it. The response may be empty or end in a tool call: callers use
// that shape to label work as partial instead of pretending it was answered.
func LastExchange(entries []Entry) (prompt, response []Entry, ok bool) {
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].Role != RoleUser {
			continue
		}
		prompt = append([]Entry(nil), entries[index])
		response = append([]Entry(nil), entries[index+1:]...)
		return prompt, response, true
	}
	return nil, nil, false
}
