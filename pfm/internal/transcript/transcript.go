// Package transcript parses what a chat actually said, from the engine's own
// transcript file — a Claude registry jsonl or a Codex rollout jsonl.
//
// It is the single parser in this tree: reading a chat off its tmux pane loses
// everything that scrolled, blurs a dead chat into a quiet one, and breaks the
// day an engine repaints. The file is the record.
package transcript

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/naming"
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
		if parsed.Type == "user" && naming.IsJunkPrompt(text) {
			return Entry{}, false
		}
		return Entry{
			Role:      parsed.Type,
			Text:      text,
			Timestamp: parsed.Timestamp,
		}, true
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
	}

	role := ""
	var content json.RawMessage
	switch parsed.Payload.Type {
	case "user_message":
		role = RoleUser
		content = parsed.Payload.Message
	case "agent_message":
		role = RoleAssistant
		content = parsed.Payload.Message
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
// assistant, U for the human, S for a compaction summary.
func Condensed(entry Entry) string {
	switch entry.Role {
	case RoleTool:
		return "T " + entry.Tool + "|" + flatten(Truncate(entry.Input, ToolInputCap))
	case RoleAssistant:
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
	for !utf8.ValidString(value) && len(value) > 0 {
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
