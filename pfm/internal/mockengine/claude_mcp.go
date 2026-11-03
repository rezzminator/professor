package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (session *claudeSession) mcp(step Step) error {
	if step.Tool == "" {
		return fmt.Errorf("claude MCP client calls are not scripted here: pfm registers its servers for Claude " +
			"through the launch registry (internal/claudelaunch/render.go) and no pfm reader consumes the handshake")
	}
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	use := contentPart{Type: blockToolUse, ID: "toolu_" + uuid.NewV4().String()[:8], Name: step.Tool, Input: input}
	if err := session.write(
		session.assistantRecord([]contentPart{use}, session.proc.script.Tokens, false),
	); err != nil {
		return err
	}
	answer, callErr := session.callMCP(step, input)
	if callErr != nil {
		answer = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: callErr.Error()}}}
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("encode MCP %s result: %w", step.Tool, err)
	}
	if err := session.proc.recorder.write("mcp-call-"+step.Tool+".json", string(encoded)+"\n"); err != nil {
		return err
	}
	result := contentPart{Type: "tool_result", ToolUseID: use.ID, Content: string(encoded), IsError: answer.IsError}
	return session.write(claudeRecord{
		Type: roleUser, SessionID: session.sessionID, Timestamp: stamp(time.Now()), CWD: session.proc.cwd,
		Message: &claudeMessage{Role: roleUser, Content: []contentPart{result}},
	})
}

func (session *claudeSession) callMCP(step Step, input json.RawMessage) (*mcp.CallToolResult, error) {
	name := mcpServerName(step)
	config := session.call.mcpConfig
	content := []byte(config)
	if !strings.HasPrefix(strings.TrimSpace(config), "{") {
		path := config
		if !filepath.IsAbs(path) && path != "" {
			path = filepath.Join(session.proc.cwd, path)
		}
		var err error
		content, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s %q: %w", flagMCPConfig, path, err)
		}
	}
	var document struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("decode %s %q: %w", flagMCPConfig, config, err)
	}
	server, present := document.Servers[name]
	if !present || server.Command == "" || (server.Type != "" && server.Type != "stdio") {
		return nil, fmt.Errorf("%s %q has no stdio mcpServers.%s command", flagMCPConfig, config, name)
	}
	environ, err := session.mcpEnvironment()
	if err != nil {
		return nil, err
	}
	step.Input = input
	return session.proc.callLocalMCP(step, append([]string{server.Command}, server.Args...), environ, localMCPOptions{
		InitializeError: fmt.Sprintf("initialize MCP %s with %s", name, server.Command),
		CloseWarning:    fmt.Sprintf("close MCP %s session: %%v", strings.ReplaceAll(name, "%", "%%")),
	})
}
