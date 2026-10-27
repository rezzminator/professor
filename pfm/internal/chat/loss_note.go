package chat

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

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const (
	lostUnanswered = "your last message was not answered — re-send it"
	lostSubagent   = "a sub-agent was running and ended"
	lostUnknown    = "restart loss unknown"
)

// lossNote streams the producer history, retaining raw tool IDs across all
// blocks. Normalized entries deliberately omit results and cannot pair tools.
func lossNote(ctx context.Context, path string, engine pfmengine.ID) (note string, returnErr error) {
	if engine == pfmengine.OpenCode {
		return "", errors.New("OpenCode restart loss is unknown: its SQLite messages are not a transcript file")
	}
	if engine != pfmengine.Claude && engine != pfmengine.Codex {
		return "", fmt.Errorf("restart loss unsupported for engine %q", engine)
	}
	if path == "" {
		return "", errors.New("restart loss unknown: transcript path is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open restart transcript: %w", err)
	}
	pending := make(map[string]bool)
	unanswered := false
	defer func() {
		returnErr = errors.Join(returnErr, file.Close())
		var notes []string
		if unanswered {
			notes = append(notes, lostUnanswered)
		}
		if len(pending) > 0 {
			notes = append(notes, lostSubagent)
		}
		if returnErr != nil {
			notes = append(notes, lostUnknown)
		}
		note = strings.Join(notes, "; ")
	}()
	var damage error
	reader := bufio.NewReader(file)
	for lineNo := 1; ; lineNo++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("read restart transcript: %w", err)
		}
		line, readErr := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			switch {
			case !json.Valid(line):
				damage = errors.Join(
					damage,
					fmt.Errorf("restart transcript line %d is malformed or incomplete", lineNo),
				)
			case engine == pfmengine.Claude:
				if err := lossClaude(line, pending, &unanswered); err != nil {
					damage = errors.Join(damage, fmt.Errorf("restart transcript line %d: %w", lineNo, err))
				}
			default:
				record, ok := transcript.DecodeCodexRecord(line)
				if !ok {
					return "", fmt.Errorf("restart transcript line %d is not an object", lineNo)
				}
				if _, ok := record.Kind(); !ok {
					return "", fmt.Errorf("restart transcript line %d has non-string type", lineNo)
				}
				var shape struct {
					Payload map[string]json.RawMessage `json:"payload"`
				}
				if err := json.Unmarshal(line, &shape); err != nil || shape.Payload == nil {
					return "", fmt.Errorf("restart transcript line %d has unsupported Codex payload", lineNo)
				}
				if entry, ok := transcript.Parse(line, string(engine)); ok {
					switch entry.Role {
					case transcript.RoleUser:
						unanswered = true
					case transcript.RoleAssistant, transcript.RoleTool:
						unanswered = false
					}
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return "", fmt.Errorf("read restart transcript: %w", readErr)
			}
			break
		}
	}
	return "", damage
}

func lossClaude(line []byte, pending map[string]bool, unanswered *bool) error {
	record, ok := transcript.DecodeClaudeRecord(line)
	if !ok {
		return errors.New("record is not an object")
	}
	kind, ok := record.Kind()
	if !ok {
		return errors.New("record has non-string type")
	}
	var flags struct {
		Sidechain bool `json:"isSidechain"`
		Meta      bool `json:"isMeta"`
	}
	if err := json.Unmarshal(line, &flags); err != nil {
		return errors.New("record metadata has unsupported shape")
	}
	if flags.Sidechain || flags.Meta || kind != transcript.RoleUser && kind != transcript.RoleAssistant {
		return nil
	}
	message := record.DecodeMessage()
	if message == nil {
		return errors.New("message is missing or not an object")
	}
	content := bytes.TrimSpace(message.Content)
	if len(content) == 0 || bytes.Equal(content, []byte("null")) {
		return errors.New("message content is unavailable")
	}
	if _, ok := pyvalue.String(content); ok {
		if kind == transcript.RoleUser {
			*unanswered = true
		} else {
			*unanswered = false
		}
		return nil
	}
	if content[0] == '{' {
		content = append(append([]byte{'['}, content...), ']')
	}
	blocks, ok := transcript.DecodeContent(content)
	if !ok {
		return errors.New("message content has unsupported shape")
	}
	human := false
	var damage error
	if kind == transcript.RoleAssistant {
		*unanswered = false
	}
	for i := range blocks {
		block := &blocks[i]
		if !block.IsObject() {
			return errors.New("message content contains a non-object block")
		}
		switch block.Kind() {
		case "text", "input_text", "output_text", "image", "document":
			human = true
		case "tool_use":
			name, ok := pyvalue.String(block.Name)
			if !ok {
				return errors.New("tool_use name is not a string")
			}
			if name == "Agent" || name == "Task" {
				id, ok := pyvalue.String(block.ID)
				if !ok || id == "" {
					return errors.New("sub-agent tool_use has no usable id")
				}
				pending[id] = true
			}
		case "tool_result":
			id, ok := pyvalue.String(block.ToolUseID)
			if !ok || id == "" {
				return errors.New("tool_result has no usable tool_use_id")
			}
			delete(pending, id)
		case "thinking",
			"redacted_thinking",
			"tool_reference",
			"server_tool_use",
			"web_search_tool_result",
			"web_fetch_tool_result":
		default:
			damage = errors.Join(damage, errors.New("message content contains an unsupported block type"))
		}
	}
	if kind == transcript.RoleUser && human {
		*unanswered = true
	}
	return damage
}

func lossOpenCode(ctx context.Context, root, id string) (string, error) {
	rows, err := index.ReadOpenCodeSessionRows(ctx, root, id)
	if err != nil {
		return "", fmt.Errorf("read OpenCode restart history: %w", err)
	}
	unanswered := false
	for _, row := range rows.Messages {
		message, err := index.DecodeOpenCodeMessage(row.Data)
		if err != nil {
			return "", fmt.Errorf("read OpenCode restart message: %w", err)
		}
		switch message.Role {
		case transcript.RoleUser:
			unanswered = true
		case transcript.RoleAssistant:
			unanswered = false
		default:
			return "", errors.New("OpenCode restart message has unsupported role")
		}
	}
	agent := false
	for _, row := range rows.Parts {
		var part struct {
			Type   string `json:"type"`
			CallID string `json:"callID"`
			Tool   string `json:"tool"`
			State  struct {
				Status string `json:"status"`
			} `json:"state"`
		}
		if err := json.Unmarshal([]byte(row.Data), &part); err != nil {
			return "", fmt.Errorf("read OpenCode restart part: %w", err)
		}
		if part.Type == "" {
			return "", errors.New("OpenCode restart part has no type")
		}
		if part.Type != "tool" || part.Tool != "task" {
			continue
		}
		if part.CallID == "" {
			return "", errors.New("OpenCode sub-agent part has no callID")
		}
		switch part.State.Status {
		case "pending", "running":
			agent = true
		case "completed", "error":
		default:
			return "", errors.New("OpenCode sub-agent part has unknown state")
		}
	}
	var notes []string
	if unanswered {
		notes = append(notes, lostUnanswered)
	}
	if agent {
		notes = append(notes, lostSubagent)
	}
	return strings.Join(notes, "; "), nil
}
