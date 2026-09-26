package opencodegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
	var buildConfig openCodeBuildConfig
	if err := json.Unmarshal(raw, &buildConfig); err != nil {
		return models, fmt.Errorf("parse OpenCode compiler config %s: %w", path, err)
	}
	for alias, model := range buildConfig.ModelMap {
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

// retiredMCPServers are the per-family servers the professor server replaced;
// a source naming one of them, when no project server carries that name, is
// pointed at its professor tool instead of being granted.
var retiredMCPServers = map[string]bool{"harvester": true, "chat": true}

// openCodeKnownMCPServers names, as OpenCode sanitizes them, the servers an
// agent's tools block governs: professor, registered globally by pfm install,
// plus every mcpServers key of the project root .mcp.json. A missing or
// unparsable .mcp.json contributes none; compileConfig reports it.
func openCodeKnownMCPServers(root string) []string {
	known := map[string]bool{config.MCPServerProfessor: true}
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err == nil {
		var source struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(raw, &source) == nil {
			for name := range source.MCPServers {
				if server, _, partsErr := pfmengine.OpenCodeMCPToolParts("mcp__" + name + "__tool"); partsErr == nil {
					known[server] = true
				}
			}
		}
	}
	servers := make([]string, 0, len(known))
	for server := range known {
		servers = append(servers, server)
	}
	sort.Strings(servers)
	return servers
}

// renderOpenCodeToolsBlock projects a Claude tools allow-list onto OpenCode:
// every builtin outside the list is denied, every known MCP server is denied
// unless a mcp__<server>__* wildcard grants it, and each listed tool of a known
// server is granted after its server's deny. A tool it cannot map — a retired
// or unknown server, a Claude tool with no OpenCode equivalent — stays denied
// and is named in a warning against source.
func renderOpenCodeToolsBlock(source, allowList string, known []string) (string, []string, error) {
	knownServer := map[string]bool{}
	for _, server := range known {
		knownServer[server] = true
	}
	var warnings []string
	allowed := map[string]bool{}
	mcpAllowed := map[string]map[string]bool{}
	wildcard := map[string]bool{}
	for _, claudeTool := range strings.Split(allowList, ",") {
		claudeTool = strings.TrimSpace(claudeTool)
		if claudeTool == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(claudeTool), "mcp__") {
			openCodeTools, ok := claudeToOpenCodeTools[claudeTool]
			if !ok {
				warnings = append(warnings, fmt.Sprintf(
					"unmapped Claude tool %s in %s — no OpenCode equivalent; it stays denied", claudeTool, source))
			}
			for _, openCodeTool := range openCodeTools {
				allowed[openCodeTool] = true
			}
			continue
		}
		isWildcard := strings.HasSuffix(claudeTool, "__*")
		parseName := claudeTool
		if isWildcard {
			parseName = strings.TrimSuffix(claudeTool, "*") + "all"
		}
		server, tool, err := pfmengine.OpenCodeMCPToolParts(parseName)
		if err != nil {
			return "", nil, err
		}
		if !knownServer[server] {
			if retiredMCPServers[server] {
				replacement := tool
				if isWildcard {
					replacement = "*"
				}
				if !strings.HasPrefix(replacement, server+"_") {
					replacement = server + "_" + replacement
				}
				warnings = append(warnings, fmt.Sprintf(
					"retired MCP tool %s in %s — the professor server serves it as mcp__%s__%s",
					claudeTool, source, config.MCPServerProfessor, replacement))
			} else {
				warnings = append(warnings, fmt.Sprintf(
					"unknown MCP server in tool %s in %s — neither %s nor a project .mcp.json server serves it; it stays denied",
					claudeTool,
					source,
					config.MCPServerProfessor,
				))
			}
			continue
		}
		if isWildcard {
			wildcard[server] = true
			continue
		}
		if mcpAllowed[server] == nil {
			mcpAllowed[server] = map[string]bool{}
		}
		mcpAllowed[server][server+"_"+tool] = true
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
	servers := append([]string(nil), known...)
	sort.Strings(servers)
	for _, server := range servers {
		if wildcard[server] {
			fmt.Fprintf(&content, "  %s_*: true\n", server)
			continue
		}
		fmt.Fprintf(&content, "  %s_*: false\n", server)
		ids := make([]string, 0, len(mcpAllowed[server]))
		for id := range mcpAllowed[server] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(&content, "  %s: true\n", id)
		}
	}
	return content.String(), warnings, nil
}
