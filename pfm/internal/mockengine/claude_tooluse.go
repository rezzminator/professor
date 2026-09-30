package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (session *claudeSession) postToolUse(
	tool string, input json.RawMessage, useID string, duration time.Duration,
) error {
	response, err := session.toolResponse(tool, input)
	if err != nil {
		return err
	}
	payload := session.payload(hookPostToolUse)
	payload.ToolName, payload.ToolInput, payload.ToolUseID = tool, input, useID
	payload.ToolResponse = response
	ms := duration.Milliseconds()
	payload.DurationMS = &ms
	_, err = session.fire(hookPostToolUse, tool, payload)
	return err
}

func (session *claudeSession) toolResponse(tool string, input json.RawMessage) (json.RawMessage, error) {
	if tool != "Edit" && tool != "Write" {
		return json.RawMessage(`{}`), nil
	}
	var fields struct {
		FilePath   string `json:"file_path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		Content    string `json:"content"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return nil, fmt.Errorf("decode %s input: %w", tool, err)
	}
	path := fields.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(session.proc.cwd, path)
	}
	data, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var original any
	if exists {
		original = string(data)
	}
	response := map[string]any{
		"filePath": fields.FilePath, "originalFile": original,
		"structuredPatch": []any{}, "userModified": false,
	}
	if tool == "Edit" {
		response["oldString"], response["newString"], response["replaceAll"] = fields.OldString, fields.NewString, fields.ReplaceAll
	} else {
		kind := "create"
		if exists {
			kind = "update"
		}
		response["type"], response["content"] = kind, fields.Content
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode %s response: %w", tool, err)
	}
	return encoded, nil
}
