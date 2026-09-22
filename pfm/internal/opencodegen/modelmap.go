package opencodegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	openCodeBuildConfigPath = ".claude/opencode-build.json"
	openCodeAgentMode       = "all"
	openCodeApplyPatchTool  = "apply_patch"
	openCodeBashTool        = "bash"
	openCodeEditTool        = "edit"
	openCodeTaskTool        = "task"
	openCodeTodoWriteTool   = "todowrite"
)

type openCodeBuildConfig struct {
	ModelMap map[string]string `json:"modelMap"`
}

func defaultOpenCodeModelMap() map[string]string {
	return map[string]string{
		"opus":   "openai/gpt-5.6-sol",
		"sonnet": "openai/gpt-5.6-sol-fast",
		"haiku":  "openai/gpt-5.5-fast",
		"fable":  "openai/gpt-5.6-sol",
	}
}

func loadOpenCodeModelMap(root string) (map[string]string, error) {
	models := defaultOpenCodeModelMap()
	path := filepath.Join(root, filepath.FromSlash(openCodeBuildConfigPath))
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return models, nil
	}
	if err != nil {
		return models, fmt.Errorf("read OpenCode compiler config %s: %w", path, err)
	}
	var config openCodeBuildConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return models, fmt.Errorf("parse OpenCode compiler config %s: %w", path, err)
	}
	for alias, model := range config.ModelMap {
		models[alias] = model
	}
	return models, nil
}

var claudeToOpenCodeTools = map[string][]string{
	"Agent":           {openCodeTaskTool},
	"AskUserQuestion": {"question"},
	"Bash":            {openCodeBashTool},
	"Edit":            {openCodeEditTool, openCodeApplyPatchTool},
	"Glob":            {"glob"},
	"Grep":            {"grep"},
	"LSP":             {"lsp"},
	"Read":            {"read"},
	"SendMessage":     {openCodeTaskTool},
	"Skill":           {"skill"},
	"TodoRead":        {openCodeTodoWriteTool},
	"TodoWrite":       {openCodeTodoWriteTool},
	"WebFetch":        {"webfetch"},
	"WebSearch":       {"websearch"},
	"Write":           {"write", openCodeApplyPatchTool},
}

var openCodeToolIDs = []string{
	openCodeApplyPatchTool,
	openCodeBashTool,
	openCodeEditTool,
	"execute",
	"glob",
	"grep",
	"invalid",
	"lsp",
	"plan_exit",
	"question",
	"read",
	"skill",
	openCodeTaskTool,
	openCodeTodoWriteTool,
	"webfetch",
	"websearch",
	"write",
}

func renderOpenCodeToolsBlock(allowList string) string {
	allowed := map[string]bool{}
	for _, claudeTool := range strings.Split(allowList, ",") {
		for _, openCodeTool := range claudeToOpenCodeTools[strings.TrimSpace(claudeTool)] {
			allowed[openCodeTool] = true
		}
	}
	denied := make([]string, 0, len(openCodeToolIDs))
	for _, tool := range openCodeToolIDs {
		if !allowed[tool] {
			denied = append(denied, tool)
		}
	}
	sort.Strings(denied)
	var content strings.Builder
	content.WriteString("tools:\n")
	for _, tool := range denied {
		fmt.Fprintf(&content, "  %s: false\n", tool)
	}
	return content.String()
}
