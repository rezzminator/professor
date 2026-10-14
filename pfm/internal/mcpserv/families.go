package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

// FamilyServerOptions names the family services the daemon carries.
type FamilyServerOptions struct {
	Version   string
	Chat      *Service            // nil: the chat family is disabled
	Harvester *harvestmcp.Service // nil: the harvester family is disabled
	// Failed holds each enabled family that failed to configure. Its tools
	// stay listed under their normal names and every call answers an MCP
	// error result naming the family, the error and the fix.
	Failed []FailedFamily
}

// FailedFamily is one enabled family whose configuration failed.
type FailedFamily struct {
	Family     string   // pfmconfig.MCPServerChat or pfmconfig.MCPServerHarvester
	Tools      []string // the names the family registers when healthy
	Err        error    // the configuration error, rendered verbatim
	ConfigPath string   // the pfm config file holding the key to correct
}

// FamilyServers serves each enabled family with its own identity and tools.
type FamilyServers struct {
	views map[string]*mcp.Server
	chat  *Service // nil: the chat family is disabled or failed
	// failed names each enabled family that failed to configure: its tools
	// answer the error in process, so a stdio server holding one never
	// forwards to a daemon that would serve that family healthy.
	failed   []string
	families map[string]http.Handler
	servers  map[string][]string
}

// NewFamilyServers builds one server per enabled family. A
// professor whose every enabled family failed still builds: its tools answer
// the configuration errors.
func NewFamilyServers(options FamilyServerOptions) (*FamilyServers, error) {
	if options.Chat == nil && options.Harvester == nil && len(options.Failed) == 0 {
		return nil, errors.New("pfm mcp: every family is disabled")
	}
	professor := &FamilyServers{
		chat:     options.Chat,
		views:    map[string]*mcp.Server{},
		families: map[string]http.Handler{},
		servers:  map[string][]string{},
	}
	add := func(family, instructions string, register func(*mcp.Server), roster []string) {
		view := newFamilyServer(family, options.Version, instructions)
		register(view)
		professor.families[family] = newMCPHTTPHandler(view, pfmconfig.MCPRegistrationName(family))
		professor.servers[family] = roster
		professor.views[family] = view
	}
	if options.Harvester != nil {
		add(pfmconfig.MCPServerHarvester, options.Harvester.Instructions(),
			options.Harvester.RegisterTools, options.Harvester.ToolNames())
	}
	if options.Chat != nil {
		add(pfmconfig.MCPServerChat, chatInstructions, options.Chat.registerTools, ToolNames())
	}
	for _, failed := range options.Failed {
		professor.failed = append(professor.failed, failed.Family)
		add(failed.Family, failedFamilyInstructions(failed.Family), failed.registerTools, failed.Tools)
	}
	return professor, nil
}

func newFamilyServer(family, version, instructions string) *mcp.Server {
	return mcp.NewServer(
		&mcp.Implementation{Name: pfmconfig.MCPRegistrationName(family), Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)
}

// Server returns one enabled family's SDK server.
func (professor *FamilyServers) Server(family string) *mcp.Server { return professor.views[family] }

// FamilyHandler serves one family's view at config.MCPFamilyPath(family); nil
// when that family is disabled.
func (professor *FamilyServers) FamilyHandler(family string) http.Handler {
	handler, ok := professor.families[family]
	if !ok {
		return nil
	}
	return handler
}

// Servers maps each enabled family to its tool roster.
func (professor *FamilyServers) Servers() map[string][]string {
	servers := make(map[string][]string, len(professor.servers))
	for family, roster := range professor.servers {
		servers[family] = append([]string(nil), roster...)
	}
	return servers
}

func failedFamilyInstructions(family string) string {
	return fmt.Sprintf(
		"The %s family failed to configure: every %s tool answers the error and its fix.",
		family,
		family,
	)
}

// registerTools lists each of the family's tools under its normal name; every
// call answers the configuration error, so the tools are never silently absent.
func (failed FailedFamily) registerTools(server *mcp.Server) {
	message := fmt.Sprintf(
		"pfm mcp: the %s family failed to configure: %v. Fix: correct the config key this error names in %s, then reconnect the MCP server (Claude Code: /mcp; Codex and OpenCode: start a new chat).",
		failed.Family,
		failed.Err,
		failed.ConfigPath,
	)
	handler := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}, nil
	}
	for _, name := range failed.Tools {
		server.AddTool(&mcp.Tool{
			Name:        name,
			Description: message,
			InputSchema: map[string]any{"type": "object"},
		}, handler)
	}
}
