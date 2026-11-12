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

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// claudeSessionEnv is the variable Claude Code hands its tool shells the chat's session in.
var claudeSessionEnv = pfmengine.MustLookup(pfmengine.Claude).SessionEnv

// tool plays a tool_call: PreToolUse, the tool_use record, the call performed when the step says so and
// no hook denied it, PostToolUse, then the tool_result; it returns the deny reason, "" when allowed.
func (session *claudeSession) tool(step Step) (string, error) {
	use, err := session.useTool(step)
	if err != nil {
		return "", err
	}
	return session.finishTool(step, use)
}

// toolUse is a tool call between its tool_use record and its result: the record's part, the PreToolUse
// answers and when that hook returned.
type toolUse struct {
	part        contentPart
	answers     []hookAnswer
	preReturned time.Time
}

// denied is the reason the first PreToolUse answer that denies the call gives; found is false when none does.
func (use toolUse) denied() (reason string, found bool) {
	for index := range use.answers {
		if refused, reason := use.answers[index].denies(); refused {
			return reason, true
		}
	}
	return "", false
}

// useTool fires PreToolUse and writes the tool_use record.
func (session *claudeSession) useTool(step Step) (toolUse, error) {
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	payload := session.payload(hookPreToolUse)
	payload.ToolName, payload.ToolInput = step.Tool, input
	part := contentPart{Type: "tool_use", ID: "toolu_" + uuid.NewV4().String()[:8], Name: step.Tool, Input: input}
	payload.ToolUseID = part.ID
	answers, err := session.fire(hookPreToolUse, step.Tool, payload)
	if err != nil {
		return toolUse{}, err
	}
	use := toolUse{part: part, answers: answers, preReturned: time.Now()}
	return use, session.write(session.assistantRecord([]contentPart{part}, session.proc.script.Tokens, false))
}

// finishTool performs a used tool no hook denied, fires PostToolUse and writes the tool_result; it returns
// the deny reason, "" when allowed.
func (session *claudeSession) finishTool(step Step, use toolUse) (string, error) {
	input := use.part.Input
	result := contentPart{Type: blockToolResult, ToolUseID: use.part.ID, Content: "done"}
	denied, deniedFound := use.denied()
	if deniedFound {
		result.Content, result.IsError = denied, true
	} else {
		// The response reads the file a Write replaces, so it is taken before the call is performed.
		response, err := session.toolResponse(step.Tool, input)
		if err != nil {
			return "", err
		}
		if step.Perform {
			if result.Content, result.IsError, err = performTool(
				session.proc, step.Tool, input,
				append(append([]string(nil), session.environ...), claudeSessionEnv+"="+session.sessionID),
				session.toolShell,
			); err != nil {
				return "", err
			}
		}
		if err := session.postToolUse(step.Tool, input, response, use); err != nil {
			return "", err
		}
	}
	return denied, session.toolResult(result)
}

// toolResult writes the user record carrying a tool's result.
func (session *claudeSession) toolResult(result contentPart) error {
	return session.write(claudeRecord{
		Type: roleUser, SessionID: session.sessionID, Timestamp: stamp(time.Now()), CWD: session.proc.cwd,
		Message: &claudeMessage{Role: roleUser, Content: []contentPart{result}},
	})
}

// postToolUse fires PostToolUse for a performed call, its duration counted from PreToolUse's return.
func (session *claudeSession) postToolUse(tool string, input, response json.RawMessage, use toolUse) error {
	payload := session.payload(hookPostToolUse)
	payload.ToolName, payload.ToolInput, payload.ToolUseID = tool, input, use.part.ID
	payload.ToolResponse = response
	ms := time.Since(use.preReturned).Milliseconds()
	payload.DurationMS = &ms
	_, err := session.fire(hookPostToolUse, tool, payload)
	return err
}

// performTool carries out an allowed Bash or Write call (Step.Perform) the way Claude Code and Codex do:
// Bash runs the command in the engine's directory with the argv shell gives (sh -c when nil), its
// combined output the result, and a failed exit is an error result headed by its code; Write creates the
// file's directory and writes the content. env is the Bash command's environment, the engine's own when
// nil: Claude Code hands its tool shells the chat's session in CLAUDE_CODE_SESSION_ID.
func performTool(
	proc *process, tool string, input json.RawMessage, env []string, shell func(string) ([]string, error),
) (content string, failed bool, err error) {
	var fields struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return "", false, fmt.Errorf("decode %s input to perform: %w", tool, err)
	}
	if tool == "Bash" {
		if shell == nil {
			shell = shellArgv
		}
		argv, err := shell(fields.Command)
		if err != nil {
			return "", false, err
		}
		bounded, cancel := context.WithTimeout(proc.ctx, time.Minute)
		defer cancel()
		cmd := exec.CommandContext(bounded, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = proc.cwd, env
		output, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Sprintf("Exit code %d\n%s", exit.ExitCode(), output), true, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("perform Bash %q in %s: %w", fields.Command, proc.cwd, err)
		}
		return string(output), false, nil
	}
	path := fields.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(proc.cwd, path)
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
