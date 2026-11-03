package mockengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

var mcpTimeout = 30 * time.Second

func mcpBoundedContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, mcpTimeout)
}

type localMCPOptions struct {
	ListTools       bool
	InitializeError string
	CloseWarning    string
}

func mcpServerName(step Step) string {
	if step.Server != "" {
		return step.Server
	}
	return pfmconfig.MCPRegistrationName(pfmconfig.MCPServerChat)
}

func (proc *process) callLocalMCP(
	step Step,
	command, environment []string,
	options localMCPOptions,
) (*mcp.CallToolResult, error) {
	if len(command) == 0 || command[0] == "" {
		return nil, errors.New("local MCP command must name an executable")
	}
	name := mcpServerName(step)
	launched := strings.Join(command, " ")
	if options.InitializeError == "" {
		options.InitializeError = "initialize against " + launched
	}
	if options.CloseWarning == "" {
		options.CloseWarning = "close MCP session: %v"
	}
	bounded, cancel := mcpBoundedContext(proc.ctx)
	defer cancel()
	child := exec.CommandContext(bounded, command[0], command[1:]...)
	child.Dir = proc.cwd
	var err error
	child.Env, err = proc.actionEnvironment(environment)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mock-engine", Version: proc.script.Version}, nil)
	connection, err := client.Connect(bounded, &mcp.CommandTransport{Command: child}, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", options.InitializeError, err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			warn(proc.stderr, options.CloseWarning, err)
		}
	}()
	if options.ListTools {
		tools, err := connection.ListTools(bounded, nil)
		if err != nil {
			return nil, fmt.Errorf("tools/list against %s: %w", launched, err)
		}
		names := make([]string, 0, len(tools.Tools))
		for _, tool := range tools.Tools {
			names = append(names, tool.Name)
		}
		encoded, err := json.Marshal(names)
		if err != nil {
			return nil, fmt.Errorf("encode tool names: %w", err)
		}
		if err := proc.recorder.write("mcp-"+name+".json", string(encoded)+"\n"); err != nil {
			return nil, err
		}
	}
	if step.Tool == "" {
		return nil, nil
	}
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	trace, err := proc.actionTrace()
	if err != nil {
		return nil, err
	}
	answer, err := connection.CallTool(
		bounded,
		&mcp.CallToolParams{Meta: mcp.Meta{"pfmTrace": trace}, Name: step.Tool, Arguments: input},
	)
	if err != nil {
		return nil, fmt.Errorf("tools/call %s on MCP %s: %w", step.Tool, name, err)
	}
	return answer, nil
}
