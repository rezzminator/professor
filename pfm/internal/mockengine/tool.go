package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

type toolExecution struct {
	Response json.RawMessage
	Text     string
	Failure  string
}

func (proc *process) executeTool(tool string, input json.RawMessage, toolEnvironment ...string) (toolExecution, error) {
	switch tool {
	case "Bash", "bash", "shell_command", "exec_command":
		return proc.shellTool(input, toolEnvironment...)
	case "Write", "Edit", openCodeWriteTool, "edit":
		return proc.fileTool(tool, input)
	case "AskUserQuestion":
		return toolExecution{Response: input, Text: string(input)}, nil
	default:
		return toolExecution{Response: json.RawMessage(`{}`), Text: toolResultDone}, nil
	}
}

func (proc *process) fileTool(tool string, input json.RawMessage) (toolExecution, error) {
	var fields struct {
		FilePath      string `json:"file_path"`
		NativePath    string `json:"filePath"`
		OldString     string `json:"old_string"`
		NativeOld     string `json:"oldString"`
		NewString     string `json:"new_string"`
		NativeNew     string `json:"newString"`
		Content       string `json:"content"`
		ReplaceAll    bool   `json:"replace_all"`
		NativeReplace bool   `json:"replaceAll"`
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return toolExecution{}, fmt.Errorf("decode %s input: %w", tool, err)
	}
	if fields.FilePath == "" {
		fields.FilePath = fields.NativePath
	}
	if fields.FilePath == "" {
		return toolExecution{}, fmt.Errorf("%s requires file_path", tool)
	}
	if tool == "edit" {
		fields.OldString, fields.NewString, fields.ReplaceAll = fields.NativeOld, fields.NativeNew, fields.NativeReplace
	}
	path := fields.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(proc.cwd, path)
	}
	data, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return toolExecution{}, fmt.Errorf("read %s: %w", path, err)
	}
	var original any
	if exists {
		original = string(data)
	}
	response := map[string]any{
		"filePath":        fields.FilePath,
		"originalFile":    original,
		"structuredPatch": []any{},
		"userModified":    false,
	}
	content := fields.Content
	if strings.EqualFold(tool, "Edit") {
		count := strings.Count(string(data), fields.OldString)
		if !exists || fields.OldString == "" || count == 0 || (count > 1 && !fields.ReplaceAll) {
			return toolExecution{
				Failure: "Edit requires an existing file and a unique old_string (or replace_all)",
			}, nil
		}
		limit := 1
		if fields.ReplaceAll {
			limit = -1
		}
		content = strings.Replace(string(data), fields.OldString, fields.NewString, limit)
		response["oldString"], response["newString"], response["replaceAll"] = fields.OldString, fields.NewString, fields.ReplaceAll
	} else {
		kind := "create"
		if exists {
			kind = "update"
		}
		response["type"], response["content"] = kind, content
	}
	mode := os.FileMode(0o600)
	if exists {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return toolExecution{}, statErr
		}
		mode = info.Mode().Perm()
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return toolExecution{}, err
		}
	}
	if err := atomicfile.Write(path, []byte(content), mode); err != nil {
		return toolExecution{}, err
	}
	raw, err := json.Marshal(response)
	return toolExecution{Response: raw, Text: toolResultDone}, err
}
