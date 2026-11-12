package mockengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// tool plays a tool_call: PreToolUse, the tool_use record, the call performed when the step says so and
// no hook denied it, PostToolUse, then the tool_result; it returns the deny reason, "" when allowed.
func (session *claudeSession) tool(step Step) (string, error) {
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	payload := session.payload(hookPreToolUse)
	payload.ToolName, payload.ToolInput = step.Tool, input
	use := contentPart{Type: "tool_use", ID: "toolu_" + uuid.NewV4().String()[:8], Name: step.Tool, Input: input}
	payload.ToolUseID = use.ID
	answers, err := session.fire(hookPreToolUse, step.Tool, payload)
	if err != nil {
		return "", err
	}
	preReturned := time.Now()
	if err := session.write(
		session.assistantRecord([]contentPart{use}, session.proc.script.Tokens, false),
	); err != nil {
		return "", err
	}
	result := contentPart{Type: "tool_result", ToolUseID: use.ID, Content: "done"}
	denied := ""
	deniedFound := false
	for index := range answers {
		if refused, reason := answers[index].denies(); refused && !deniedFound {
			deniedFound = true
			denied = reason
			result.Content, result.IsError = reason, true
		}
	}
	if !deniedFound {
		// The response reads the file a Write replaces, so it is taken before the call is performed.
		response, err := session.toolResponse(step.Tool, input)
		if err != nil {
			return "", err
		}
		if step.Perform {
			if result.Content, result.IsError, err = session.perform(step.Tool, input); err != nil {
				return "", err
			}
		}
		if err := session.postToolUse(step.Tool, input, response, use.ID, time.Since(preReturned)); err != nil {
			return "", err
		}
	}
	record := claudeRecord{
		Type: roleUser, SessionID: session.sessionID, Timestamp: stamp(time.Now()), CWD: session.proc.cwd,
		Message: &claudeMessage{Role: roleUser, Content: []contentPart{result}},
	}
	return denied, session.write(record)
}

func (session *claudeSession) postToolUse(
	tool string, input, response json.RawMessage, useID string, duration time.Duration,
) error {
	payload := session.payload(hookPostToolUse)
	payload.ToolName, payload.ToolInput, payload.ToolUseID = tool, input, useID
	payload.ToolResponse = response
	ms := duration.Milliseconds()
	payload.DurationMS = &ms
	_, err := session.fire(hookPostToolUse, tool, payload)
	return err
}

// perform carries out an allowed Bash or Write call (Step.Perform) the way Claude Code does: Bash runs
// the command with sh in the engine's directory, its combined output the result, and a failed exit is
// an error result headed by its code; Write creates the file's directory and writes the content.
func (session *claudeSession) perform(tool string, input json.RawMessage) (content string, failed bool, err error) {
	var fields struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return "", false, fmt.Errorf("decode %s input to perform: %w", tool, err)
	}
	if tool == "Bash" {
		shell, err := deps.Resolve("sh")
		if err != nil {
			return "", false, fmt.Errorf("perform Bash: sh not found: %w", err)
		}
		bounded, cancel := context.WithTimeout(session.proc.ctx, time.Minute)
		defer cancel()
		cmd := exec.CommandContext(bounded, shell, "-c", fields.Command)
		cmd.Dir = session.proc.cwd
		output, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Sprintf("Exit code %d\n%s", exit.ExitCode(), output), true, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("perform Bash %q in %s: %w", fields.Command, session.proc.cwd, err)
		}
		return string(output), false, nil
	}
	path := fields.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(session.proc.cwd, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, fmt.Errorf("perform Write: create the directory of %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(fields.Content), 0o644); err != nil {
		return "", false, fmt.Errorf("perform Write %s: %w", path, err)
	}
	return "File created successfully at: " + path, false, nil
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

// validateToolCall refuses a tool_call with no tool, or one Perform cannot carry out.
func (step Step) validateToolCall() error {
	if step.Tool == "" {
		return errors.New("tool_call needs tool")
	}
	if step.Perform && step.Tool != "Bash" && step.Tool != "Write" {
		return fmt.Errorf("tool_call performs only Bash and Write, not %q", step.Tool)
	}
	return nil
}

// toolRows are the pane rows of a played tool call: its line, or its deny with each later line of the
// reason a row of its own under the first.
func toolRows(tool, denied string) []string {
	if denied == "" {
		return []string{"⏺ " + tool + "(…)"}
	}
	lines := strings.Split(strings.TrimRight(denied, "\n"), "\n")
	rows := []string{"⏺ " + tool + " — denied by hook: " + lines[0]}
	for _, line := range lines[1:] {
		rows = append(rows, "  "+line)
	}
	return rows
}
