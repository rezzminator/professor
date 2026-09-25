package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

// professorRoute labels the combined server's http.in records; a family view
// logs under professorRoute + "-" + family.
const professorRoute = "professor-mcp"

// ProfessorOptions names the family services the professor server carries.
type ProfessorOptions struct {
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

// Professor is the one MCP server the daemon serves: the combined server
// carrying every enabled family, plus one view per enabled family. Every
// server's tools are bound to the same service values, so there is one SQLite
// handle and one harvester behind all of them.
type Professor struct {
	combined *mcp.Server
	chat     *Service // nil: the chat family is disabled or failed
	// chatFailed: the chat family is enabled but failed to configure, so no
	// runtime identity exists to check a daemon's chat against.
	chatFailed bool
	handler    http.Handler
	families   map[string]http.Handler
	servers    map[string][]string
}

// NewProfessor builds the combined server and one view per enabled family.
func NewProfessor(options ProfessorOptions) (*Professor, error) {
	if options.Chat == nil && options.Harvester == nil {
		return nil, errors.New("pfm mcp: every family is disabled")
	}
	professor := &Professor{
		chat: options.Chat, families: map[string]http.Handler{}, servers: map[string][]string{},
	}
	var parts []string
	var registers []func(*mcp.Server)
	add := func(family, instructions string, register func(*mcp.Server), roster []string) {
		view := newProfessorServer(options.Version, instructions)
		register(view)
		professor.families[family] = newMCPHTTPHandler(view, professorRoute+"-"+family)
		professor.servers[family] = roster
		parts = append(parts, instructions)
		registers = append(registers, register)
	}
	if options.Harvester != nil {
		add(pfmconfig.MCPServerHarvester, options.Harvester.Instructions(),
			options.Harvester.RegisterTools, options.Harvester.ToolNames())
	}
	if options.Chat != nil {
		add(pfmconfig.MCPServerChat, chatInstructions, options.Chat.registerTools, ToolNames())
	}
	for _, failed := range options.Failed {
		if failed.Family == pfmconfig.MCPServerChat {
			professor.chatFailed = true
		}
		add(failed.Family, failedFamilyInstructions(failed.Family), failed.registerTools, failed.Tools)
	}
	professor.combined = newProfessorServer(options.Version, strings.Join(parts, "\n\n"))
	for _, register := range registers {
		register(professor.combined)
	}
	professor.handler = newMCPHTTPHandler(professor.combined, professorRoute)
	return professor, nil
}

func newProfessorServer(version, instructions string) *mcp.Server {
	return mcp.NewServer(
		&mcp.Implementation{Name: pfmconfig.MCPServerProfessor, Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)
}

// Server is the combined server, for in-process sessions.
func (professor *Professor) Server() *mcp.Server { return professor.combined }

// Handler serves the combined server at config.MCPPathProfessor.
func (professor *Professor) Handler() http.Handler { return professor.handler }

// FamilyHandler serves one family's view at config.MCPFamilyPath(family); nil
// when that family is disabled.
func (professor *Professor) FamilyHandler(family string) http.Handler {
	handler, ok := professor.families[family]
	if !ok {
		return nil
	}
	return handler
}

// Servers maps each enabled family to its tool roster.
func (professor *Professor) Servers() map[string][]string {
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
		"pfm mcp: the %s family failed to configure: %v. Fix: correct the config key this error names in %s, then reconnect with /mcp.",
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
