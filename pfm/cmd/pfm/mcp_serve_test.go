package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/harvestmcp"
	"hostops/pfm/internal/mcpserv"
	"hostops/pfm/internal/paths"
)

func TestMCPDaemonHandlerIsUnauthenticatedAndReportsSurface(t *testing.T) {
	handler := newMCPDaemonHandler(mcpDaemonOptions{
		Version:   "test-version",
		StartedAt: time.Unix(123, 0).UTC(),
		Endpoint:  "http://127.0.0.1:8377",
		Chat: http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		),
		Harvester: http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		),
	})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", http.NoBody))
	if response.Code != http.StatusOK {
		t.Fatalf("status without credentials = %d, body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("unauthenticated loopback service advertised auth challenge %q", got)
	}
	var status mcpserv.DaemonStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.PFMVersion != "test-version" || status.ProtocolVersion == "" || status.PID < 1 || status.Endpoint == "" {
		t.Fatalf("status = %+v", status)
	}
	if !strings.Contains(response.Body.String(), "chat") || !strings.Contains(response.Body.String(), "harvester") {
		t.Fatalf("status surface = %s", response.Body.String())
	}
}

func TestMCPDaemonRejectsBrowserOriginBeforeDispatch(t *testing.T) {
	dispatched := 0
	handler := newMCPDaemonHandler(mcpDaemonOptions{
		Chat: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			dispatched++
			w.WriteHeader(http.StatusNoContent)
		}),
	})
	browser := httptest.NewRequest(http.MethodPost, "/mcp/chat", http.NoBody)
	browser.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, browser)
	if response.Code != http.StatusForbidden || dispatched != 0 {
		t.Fatalf("browser-origin request status=%d dispatched=%d, want 403/0", response.Code, dispatched)
	}

	local := httptest.NewRequest(http.MethodPost, "/mcp/chat", http.NoBody)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, local)
	if response.Code != http.StatusNoContent || dispatched != 1 {
		t.Fatalf("origin-free local request status=%d dispatched=%d, want 204/1", response.Code, dispatched)
	}
}

func TestMCPDaemonMountedServersNeedNoAuthAndServeTools(t *testing.T) {
	root := jailTest(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	chat, err := mcpserv.NewConfigured("test", nil, mcpserv.Runtime{Paths: resolved})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := chat.Close(); err != nil {
			t.Errorf("close chat: %v", err)
		}
	}()
	harvester, err := harvestmcp.NewConfiguredHarvester("test", harvestmcp.Runtime{
		Home: root, CacheDir: root + "/cache",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := harvester.Close(); err != nil {
			t.Errorf("close harvester: %v", err)
		}
	}()

	handler := newMCPDaemonHandler(mcpDaemonOptions{
		Version: "test", StartedAt: time.Now(), Endpoint: "http://127.0.0.1:8377",
		Chat: chat.NewHTTPHandler(), Harvester: harvester.NewHTTPHandler(),
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := http.DefaultClient
	ctx := context.Background()
	chatSession, err := connectHTTPMCP(t, ctx, server.URL+"/mcp/chat", client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := chatSession.Close(); err != nil {
			t.Errorf("close chatSession: %v", err)
		}
	}()
	tools, err := chatSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
	}
	for _, name := range []string{"chat_keys", "chat_whoami", "chat_self_compact", "chat_new"} {
		if !seen[name] {
			t.Fatalf("chat tools omitted %q: %v", name, seen)
		}
	}
	if _, err := chatSession.CallTool(ctx, &mcp.CallToolParams{Name: "chat_whoami"}); err != nil {
		t.Fatalf("chat_whoami: %v", err)
	}

	harvesterSession, err := connectHTTPMCP(t, ctx, server.URL+"/mcp/harvester", client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := harvesterSession.Close(); err != nil {
			t.Errorf("close harvesterSession: %v", err)
		}
	}()
	if _, err := harvesterSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "searchCache", Arguments: map[string]any{"pattern": "never-match"},
	}); err != nil {
		t.Fatalf("searchCache: %v", err)
	}
}

// TestMCPDaemonDisabledRouteReturns503DistinctFromEnabledAndUnknownPath pins
// the #8 fix: mcp.servers.<name>.enabled=false must produce a response that
// cannot be mistaken for either a live server or a route that never existed.
// Before the fix, enabled=false and "fully live" were indistinguishable.
func TestMCPDaemonDisabledRouteReturns503DistinctFromEnabledAndUnknownPath(t *testing.T) {
	handler := newMCPDaemonHandler(mcpDaemonOptions{
		Chat: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		// Harvester left nil: mcp.servers.harvester.enabled=false never
		// constructs a handler for it to mount.
	})

	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, httptest.NewRequest(http.MethodPost, "/mcp/harvester", http.NoBody))
	if disabled.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled route status = %d, want %d", disabled.Code, http.StatusServiceUnavailable)
	}
	wantBody := "pfm mcp: harvester is disabled by config; enable it with: pfm mcp harvester enable\n"
	if disabled.Body.String() != wantBody {
		t.Fatalf("disabled route body = %q, want %q", disabled.Body.String(), wantBody)
	}

	enabled := httptest.NewRecorder()
	handler.ServeHTTP(enabled, httptest.NewRequest(http.MethodPost, "/mcp/chat", http.NoBody))
	if enabled.Code != http.StatusNoContent {
		t.Fatalf(
			"enabled route status = %d, want %d — disabling harvester must not dark chat",
			enabled.Code,
			http.StatusNoContent,
		)
	}

	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/mcp/unknown", http.NoBody))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unregistered path status = %d, want %d", unknown.Code, http.StatusNotFound)
	}
	if unknown.Code == disabled.Code {
		t.Fatalf(
			"unregistered path and disabled server both report %d; a disabled server must read as disabled, not as an absent route",
			unknown.Code,
		)
	}
}

// TestMCPDaemonStatusServersListsOnlyMountedHandlers pins the #8 fix to
// /status: the Servers surface must name only what was actually mounted,
// never a hardcoded pair regardless of config. This is the surface the
// original defect let lie.
func TestMCPDaemonStatusServersListsOnlyMountedHandlers(t *testing.T) {
	noop := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, test := range []struct {
		name            string
		chat, harvester http.Handler
		want            []string
	}{
		{"chat only mounted", noop, nil, []string{"chat"}},
		{"harvester only mounted", nil, noop, []string{"harvester"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newMCPDaemonHandler(mcpDaemonOptions{Chat: test.chat, Harvester: test.harvester})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", http.NoBody))
			var status mcpserv.DaemonStatus
			if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if len(status.Servers) != len(test.want) {
				t.Fatalf("Servers = %v, want exactly %v", status.Servers, test.want)
			}
			for _, name := range test.want {
				if _, ok := status.Servers[name]; !ok {
					t.Fatalf("Servers = %v, missing mounted %q", status.Servers, name)
				}
			}
		})
	}
}

// TestMCPDaemonStatusHarvesterToolsFollowTheSearchGate pins the search-gate
// fix to /status: the harvester's advertised tool list must track
// harvestmcp.ToolNames for the runtime actually mounted, never a hardcoded
// six-tool list that claims `search` whether or not runtimeSearchEnabled
// holds. Before the fix, mcp_serve_command.go's package-level
// harvesterMCPTools always listed `search`; that defect is what this test
// would have caught.
func TestMCPDaemonStatusHarvesterToolsFollowTheSearchGate(t *testing.T) {
	for _, test := range []struct {
		name       string
		runtime    harvestmcp.Runtime
		wantSearch bool
	}{
		{"search disabled", harvestmcp.Runtime{Home: t.TempDir(), CacheDir: t.TempDir() + "/cache"}, false},
		{
			"search enabled",
			harvestmcp.Runtime{Home: t.TempDir(), CacheDir: t.TempDir() + "/cache", SearXNGURL: "http://searxng.example.test"},
			true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			harvester, err := harvestmcp.NewConfiguredHarvester("test", test.runtime)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := harvester.Close(); err != nil {
					t.Errorf("close harvester: %v", err)
				}
			}()
			handler := newMCPDaemonHandler(mcpDaemonOptions{
				Harvester:      harvester.NewHTTPHandler(),
				HarvesterTools: harvestmcp.RegisteredToolNames(test.runtime),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", http.NoBody))
			var status mcpserv.DaemonStatus
			if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			listed := status.Servers[config.MCPServerHarvester]
			hasSearch := false
			for _, name := range listed {
				if name == "search" {
					hasSearch = true
				}
			}
			if hasSearch != test.wantSearch {
				t.Fatalf(
					"harvester tools = %v, search listed = %v, want %v",
					listed, hasSearch, test.wantSearch,
				)
			}
		})
	}
}

// TestMCPServeBothDisabledRefusesBeforeBindingPort pins the #8 fix at the
// command layer: mcp.servers.chat.enabled=false AND
// mcp.servers.harvester.enabled=false must refuse to start with a non-zero
// exit BEFORE the port is ever bound, not start an empty daemon that answers
// nothing.
func TestMCPServeBothDisabledRefusesBeforeBindingPort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}

	runtime := commandRuntime{Config: config.Defaults(t.TempDir(), nil)}
	runtime.Config.MCP.HTTP.Port = port
	// config.Defaults leaves both chat and harvester disabled; do not enable
	// either here — that is the case under test.

	var stdout, stderr bytes.Buffer
	if code := runMCPServe(&stdout, &stderr, runtime, nil); code != 1 {
		t.Fatalf("both-disabled serve code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("both-disabled serve stdout = %q, want no startup line — nothing was mounted", stdout.String())
	}
	if !strings.Contains(stderr.String(), "every registered server is disabled by config") ||
		!strings.Contains(stderr.String(), "pfm mcp <server> enable") {
		t.Fatalf(
			"both-disabled serve stderr = %q, want the disabled-config refusal and its enable hint",
			stderr.String(),
		)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("port %d still bound after refusal: %v", port, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMCPServeRefusesHealthySecondInstance(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	portText := listener.Addr().(*net.TCPAddr).Port
	port := strconv.Itoa(portText)
	server := &http.Server{Handler: newMCPDaemonHandler(mcpDaemonOptions{
		Version: "test", StartedAt: time.Unix(5, 0), Endpoint: "http://127.0.0.1:" + port,
		Chat: http.NotFoundHandler(), Harvester: http.NotFoundHandler(),
	})}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	defer func() {
		if err := server.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve test daemon: %v", err)
		}
	}()

	runtime := commandRuntime{Config: config.Defaults(t.TempDir(), nil)}
	runtime.Config.MCP.HTTP.Port = portText
	runtime.Config.MCPServers["chat"] = config.MCPServer{Enabled: true}
	var stdout, stderr bytes.Buffer
	if code := runMCPServe(&stdout, &stderr, runtime, nil); code != 1 {
		t.Fatalf("second serve code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "already running (pid") || !strings.Contains(stderr.String(), "since") {
		t.Fatalf("second serve stderr=%q", stderr.String())
	}
}

func connectHTTPMCP(
	t *testing.T,
	ctx context.Context,
	endpoint string,
	client *http.Client,
) (*mcp.ClientSession, error) {
	t.Helper()
	protocolClient := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	return protocolClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true,
	}, nil)
}
