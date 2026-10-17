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
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

func newTestHarvester(t *testing.T, runtime harvestmcp.Runtime) *harvestmcp.Service {
	t.Helper()
	runtime.Home, runtime.CacheDir = t.TempDir(), t.TempDir()
	service, err := harvestmcp.NewConfiguredHarvester("test", runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("close harvester: %v", err)
		}
	})
	return service
}

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

var harvesterRosterNoSearch = []string{"read", "download_file", "search_literature"}

func TestProfessorFamilyViewsServeOneFamily(t *testing.T) {
	harvester := newTestHarvester(t, harvestmcp.Runtime{})
	professor := newTestProfessor(t, FamilyServerOptions{Chat: newTestChat(t), Harvester: harvester})
	for _, test := range []struct {
		family, instructions string
		roster               []string
	}{
		{pfmconfig.MCPServerHarvester, harvester.Instructions(), harvesterRosterNoSearch},
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
	t.Run("chat disabled", func(t *testing.T) {
		harvester := newTestHarvester(t, harvestmcp.Runtime{})
		professor := newTestProfessor(t, FamilyServerOptions{Harvester: harvester})
		session := professorSession(t, professor.FamilyHandler(pfmconfig.MCPServerHarvester))
		if got, want := sessionToolNames(t, session), sortedRoster(harvesterRosterNoSearch); !slices.Equal(got, want) {
			t.Fatalf("tools/list = %v, want %v", got, want)
		}
		if got := session.InitializeResult().Instructions; got != harvester.Instructions() {
			t.Fatalf("instructions = %q, want the harvester part alone", got)
		}
		if professor.FamilyHandler(pfmconfig.MCPServerChat) != nil {
			t.Fatal("the disabled chat family has a view")
		}
		if servers := professor.Servers(); len(servers) != 1 || servers[pfmconfig.MCPServerHarvester] == nil {
			t.Fatalf("Servers() = %v, want the harvester family alone", servers)
		}
	})
	t.Run("harvester disabled", func(t *testing.T) {
		professor := newTestProfessor(t, FamilyServerOptions{Chat: newTestChat(t)})
		session := professorSession(t, professor.FamilyHandler(pfmconfig.MCPServerChat))
		if got, want := sessionToolNames(t, session), sortedRoster(chatToolNames); !slices.Equal(got, want) {
			t.Fatalf("tools/list = %v, want %v", got, want)
		}
		if got := session.InitializeResult().Instructions; got != chatInstructions {
			t.Fatalf("instructions = %q, want the chat part alone", got)
		}
		if professor.FamilyHandler(pfmconfig.MCPServerHarvester) != nil {
			t.Fatal("the disabled harvester family has a view")
		}
		if servers := professor.Servers(); len(servers) != 1 || servers[pfmconfig.MCPServerChat] == nil {
			t.Fatalf("Servers() = %v, want the chat family alone", servers)
		}
	})
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
	chat := newTestChat(t)
	for _, remote := range []bool{false, true} {
		for _, search := range []bool{false, true} {
			runtime := harvestmcp.Runtime{Remote: remote}
			if search {
				runtime.SearXNGURL = "http://searxng.example.test"
			}
			harvester := newTestHarvester(t, runtime)
			families := newTestProfessor(t, FamilyServerOptions{Chat: chat, Harvester: harvester})
			for family, want := range map[string]string{pfmconfig.MCPServerChat: chatInstructions, pfmconfig.MCPServerHarvester: harvester.Instructions()} {
				text := inMemorySession(t, families.Server(family)).InitializeResult().Instructions
				if text != want || utf8.RuneCountInString(text) > 900 {
					t.Fatalf("family %s instructions = %q", family, text)
				}
			}
		}
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
	status := func(t *testing.T, options FamilyServerOptions, harvester *harvestmcp.Service) DaemonStatus {
		t.Helper()
		professor := newTestProfessor(t, options)
		daemon := DaemonOptions{
			Chat:      professor.FamilyHandler(pfmconfig.MCPServerChat),
			Harvester: professor.FamilyHandler(pfmconfig.MCPServerHarvester),
		}
		if harvester != nil {
			daemon.HarvesterTools = harvester.ToolNames()
		}
		response := httptest.NewRecorder()
		NewDaemonHandler(daemon).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", http.NoBody))
		var decoded DaemonStatus
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode /status %q: %v", response.Body.String(), err)
		}
		return decoded
	}
	chat := newTestChat(t)
	t.Run("both families, search on", func(t *testing.T) {
		harvester := newTestHarvester(t, harvestmcp.Runtime{SearXNGURL: "http://searxng.example.test"})
		servers := status(t, FamilyServerOptions{Chat: chat, Harvester: harvester}, harvester).Servers
		if !slices.Equal(servers[pfmconfig.MCPServerChat], ToolNames()) {
			t.Fatalf("servers.chat = %v, want %v", servers[pfmconfig.MCPServerChat], ToolNames())
		}
		roster := servers[pfmconfig.MCPServerHarvester]
		if len(roster) == 0 || roster[len(roster)-1] != "search_web" {
			t.Fatalf("servers.harvester = %v, want it to end with search_web", roster)
		}
	})
	t.Run("chat only", func(t *testing.T) {
		servers := status(t, FamilyServerOptions{Chat: chat}, nil).Servers
		if _, ok := servers[pfmconfig.MCPServerChat]; !ok || len(servers) != 1 {
			t.Fatalf("servers = %v, want the chat key alone", servers)
		}
	})
}
