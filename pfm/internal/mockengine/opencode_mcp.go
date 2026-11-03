package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func (session *openCodeSession) mcp(step Step) error {
	name := mcpServerName(step)
	config, err := openCodeConfig(session.proc)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(config["mcp"])
	if err != nil {
		return err
	}
	var servers map[string]struct {
		Type        string            `json:"type"`
		Command     []string          `json:"command"`
		Enabled     *bool             `json:"enabled"`
		Environment map[string]string `json:"environment"`
	}
	if err := json.Unmarshal(encoded, &servers); err != nil {
		return fmt.Errorf("decode OpenCode mcp registrations: %w", err)
	}
	server, present := servers[name]
	if !present || server.Type != "local" || len(server.Command) == 0 || server.Command[0] == "" ||
		(server.Enabled != nil && !*server.Enabled) {
		return fmt.Errorf("OpenCode config has no enabled local mcp.%s command", name)
	}
	environment := session.proc.terminalEnvironment(os.Environ())
	keys := make([]string, 0, len(server.Environment))
	for key := range server.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+server.Environment[key])
	}
	environment, err = session.proc.actionEnvironment(environment)
	if err != nil {
		return err
	}
	tool := ""
	var args any
	if step.Tool != "" {
		tool, err = pfmengine.OpenCodeMCPToolName("mcp__" + name + "__" + step.Tool)
		if err != nil {
			return err
		}
		input := step.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return fmt.Errorf("decode OpenCode MCP tool input: %w", err)
		}
		if err := session.plugin(
			"tool.execute.before",
			map[string]string{
				openCodeSessionIDKey: session.id,
				openCodeToolKey:      tool,
				openCodeCallIDKey:    "call_mcp_fixture",
			},
			map[string]any{openCodeArgsKey: args},
		); err != nil {
			return err
		}
	}
	answer, err := session.proc.callLocalMCP(
		step,
		server.Command,
		environment,
		localMCPOptions{
			ListTools:       true,
			InitializeError: "initialize against " + strings.Join(server.Command, " "),
			CloseWarning:    "close MCP session: %v",
		},
	)
	if err != nil {
		return err
	}
	if step.Tool == "" {
		return nil
	}
	result, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("encode OpenCode MCP result: %w", err)
	}
	if err := session.proc.recorder.write("mcp-call-"+step.Tool+".json", string(result)+"\n"); err != nil {
		return err
	}
	if err := session.plugin(
		"tool.execute.after",
		map[string]any{
			openCodeSessionIDKey: session.id,
			openCodeToolKey:      tool,
			openCodeCallIDKey:    "call_mcp_fixture",
			openCodeArgsKey:      args,
		},
		map[string]any{openCodeTitleKey: tool, openCodeToolOutputKey: string(result), "metadata": map[string]any{}},
	); err != nil {
		return err
	}
	return session.status("busy")
}
