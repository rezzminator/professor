package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func newTestChat(t *testing.T) *Service {
	t.Helper()
	setupBackendFixture(t)
	service := newFixtureService(t)
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close chat: %v", err)
		}
	})
	return service
}

func newTestProfessor(t *testing.T, options FamilyServerOptions) *FamilyServers {
	t.Helper()
	options.Version = "test"
	professor, err := NewFamilyServers(options)
	if err != nil {
		t.Fatal(err)
	}
	return professor
}

// professorSession connects an MCP client to handler over streamable HTTP, the
// transport the daemon mounts it on.
func professorSession(t *testing.T, handler http.Handler) *mcp.ClientSession {
	t.Helper()
	if handler == nil {
		t.Fatal("professorSession: nil handler")
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL, MaxRetries: -1, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return session
}

func sessionToolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func sortedRoster(rosters ...[]string) []string {
	var names []string
	for _, roster := range rosters {
		names = append(names, roster...)
	}
	slices.Sort(names)
	return names
}

func TestProfessorFamilyViewsServeOneFamily(t *testing.T) {
	professor := newTestProfessor(t, FamilyServerOptions{Chat: newTestChat(t)})
	for _, test := range []struct {
		family, instructions string
		roster               []string
	}{
		{pfmconfig.MCPServerChat, chatInstructions, chatToolNames},
	} {
		t.Run(test.family, func(t *testing.T) {
			session := professorSession(t, professor.FamilyHandler(test.family))
			if got, want := sessionToolNames(t, session), sortedRoster(test.roster); !slices.Equal(got, want) {
				t.Fatalf("%s view tools/list = %v, want %v", test.family, got, want)
			}
			initialized := session.InitializeResult()
			if initialized.ServerInfo.Name != test.family+"-professor" ||
				initialized.Instructions != test.instructions {
				t.Fatalf("%s view serverInfo=%q instructions=\n%q\nwant professor and\n%q",
					test.family, initialized.ServerInfo.Name, initialized.Instructions, test.instructions)
			}
		})
	}
}

func TestProfessorLeavesADisabledFamilyOut(t *testing.T) {
	professor := newTestProfessor(t, FamilyServerOptions{Chat: newTestChat(t)})
	session := professorSession(t, professor.FamilyHandler(pfmconfig.MCPServerChat))
	if got, want := sessionToolNames(t, session), sortedRoster(chatToolNames); !slices.Equal(got, want) {
		t.Fatalf("tools/list = %v, want %v", got, want)
	}
	if got := session.InitializeResult().Instructions; got != chatInstructions {
		t.Fatalf("instructions = %q", got)
	}
	if servers := professor.Servers(); len(servers) != 1 || servers[pfmconfig.MCPServerChat] == nil {
		t.Fatalf("Servers() = %v, want chat alone", servers)
	}
}

func TestProfessorRefusesWhenEveryFamilyIsDisabled(t *testing.T) {
	t.Parallel()
	professor, err := NewFamilyServers(FamilyServerOptions{Version: "test"})
	if err == nil || err.Error() != "pfm mcp: every family is disabled" || professor != nil {
		t.Fatalf("NewFamilyServers() = %v, %v; want the every-family-disabled error", professor, err)
	}
}

// A professor whose every enabled family failed to configure still starts:
// each tool of the failed family stays listed and answers IsError with the
// configuration error and a fix that names each engine's reconnect.
func TestProfessorServesAFailedFamilyAlone(t *testing.T) {
	t.Parallel()
	professor, err := NewFamilyServers(FamilyServerOptions{Version: "test", Failed: []FailedFamily{{
		Family: pfmconfig.MCPServerChat, Tools: ToolNames(),
		Err: errors.New("chat config broken"), ConfigPath: "/pfm/config.json",
	}}})
	if err != nil {
		t.Fatalf("NewFamilyServers(chat failed alone) error = %v, want a started server", err)
	}
	session := inMemorySession(t, professor.Server(pfmconfig.MCPServerChat))
	if names := sessionToolNames(t, session); !slices.Equal(names, sortedRoster(ToolNames())) {
		t.Fatalf("tools/list = %v, want every chat tool %v", names, sortedRoster(ToolNames()))
	}
	for _, name := range ToolNames() {
		result, err := session.CallTool(
			context.Background(),
			&mcp.CallToolParams{Name: name, Arguments: map[string]any{}},
		)
		if err != nil {
			t.Fatalf("%s protocol error = %v, want an MCP error result", name, err)
		}
		text := result.Content[0].(*mcp.TextContent).Text
		want := "pfm mcp: the chat family failed to configure: chat config broken. Fix: correct the config key " +
			"this error names in /pfm/config.json, then reconnect the MCP server (Claude Code: /mcp; " +
			"Codex and OpenCode: start a new chat)."
		if !result.IsError || text != want {
			t.Fatalf("%s = IsError %t %q, want IsError and %q", name, result.IsError, text, want)
		}
	}
}

func TestFamilyInstructionsStayWithinTheBound(t *testing.T) {
	families := newTestProfessor(t, FamilyServerOptions{Chat: newTestChat(t)})
	text := inMemorySession(t, families.Server(pfmconfig.MCPServerChat)).InitializeResult().Instructions
	if text != chatInstructions || utf8.RuneCountInString(text) > 900 {
		t.Fatalf("chat instructions = %q", text)
	}
}

func inMemorySession(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := serverSession.Close(); err != nil {
			t.Errorf("close server session: %v", err)
		}
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return session
}

// TestProfessorStatusReportsRostersByFamily wires a Professor into the daemon
// the way `pfm mcp serve` does and reads /status.
func TestProfessorStatusReportsRostersByFamily(t *testing.T) {
	professor := newTestProfessor(t, FamilyServerOptions{Chat: newTestChat(t)})
	response := httptest.NewRecorder()
	NewDaemonHandler(
		DaemonOptions{Chat: professor.FamilyHandler(pfmconfig.MCPServerChat)},
	).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", http.NoBody))
	var status DaemonStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Servers) != 1 || !slices.Equal(status.Servers[pfmconfig.MCPServerChat], ToolNames()) {
		t.Fatalf("servers = %v, want chat tools alone", status.Servers)
	}
}
