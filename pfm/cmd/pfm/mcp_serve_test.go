package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type deadlineScalingListener struct {
	net.Listener
	readDuration  time.Duration
	writeDuration time.Duration
}

func (listener deadlineScalingListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return deadlineScalingConn{
		Conn: connection, readDuration: listener.readDuration, writeDuration: listener.writeDuration,
	}, nil
}

type deadlineScalingConn struct {
	net.Conn
	readDuration  time.Duration
	writeDuration time.Duration
}

func (connection deadlineScalingConn) SetReadDeadline(deadline time.Time) error {
	if !deadline.IsZero() && connection.readDuration > 0 {
		deadline = time.Now().Add(connection.readDuration)
	}
	return connection.Conn.SetReadDeadline(deadline)
}

func (connection deadlineScalingConn) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() && connection.writeDuration > 0 {
		deadline = time.Now().Add(connection.writeDuration)
	}
	return connection.Conn.SetWriteDeadline(deadline)
}

func TestLoopbackMCPServerDeliversResponseAfterOldWriteDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := newLoopbackMCPServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		if _, writeErr := writer.Write([]byte("complete")); writeErr != nil {
			t.Errorf("write delayed response: %v", writeErr)
		}
	}))
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(deadlineScalingListener{
			Listener: listener, writeDuration: 25 * time.Millisecond,
		})
	}()
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("close loopback server: %v", closeErr)
		}
		if runErr := <-serveErr; runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
			t.Errorf("serve loopback server: %v", runErr)
		}
	})

	response, err := http.Get("http://" + listener.Addr().String()) //nolint:gosec // loopback test server
	if err != nil {
		t.Fatalf("get delayed response: %v", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Errorf("close delayed response: %v", closeErr)
		}
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read delayed response: %v", err)
	}
	if got := string(body); got != "complete" {
		t.Fatalf("delayed response body = %q, want complete", got)
	}
}

func TestLoopbackMCPServerPropagatesClientCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handlerStarted := make(chan struct{})
	handlerCancelled := make(chan struct{})
	server := newLoopbackMCPServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(handlerStarted)
		<-request.Context().Done()
		close(handlerCancelled)
	}))
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("close cancellation server: %v", closeErr)
		}
		if runErr := <-serveErr; runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
			t.Errorf("serve cancellation server: %v", runErr)
		}
	})

	requestContext, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(
		requestContext, http.MethodGet, "http://"+listener.Addr().String(), http.NoBody,
	)
	if err != nil {
		t.Fatal(err)
	}
	responseErr := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if response != nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				requestErr = errors.Join(requestErr, closeErr)
			}
		}
		responseErr <- requestErr
	}()
	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("request handler did not start")
	}
	cancel()
	select {
	case <-handlerCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("client cancellation did not cancel the request context")
	}
	select {
	case requestErr := <-responseErr:
		if !errors.Is(requestErr, context.Canceled) {
			t.Fatalf("cancelled request error = %v, want context.Canceled", requestErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client request did not return after cancellation")
	}
}

func TestLoopbackMCPServerRetainsSlowHeaderBound(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dispatched := make(chan struct{}, 1)
	server := newLoopbackMCPServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		dispatched <- struct{}{}
	}))
	if server.ReadHeaderTimeout != 5*time.Second || server.ReadTimeout != 30*time.Second ||
		server.IdleTimeout != 2*time.Minute {
		t.Fatalf(
			"loopback read policy = header %s/read %s/idle %s, want 5s/30s/2m",
			server.ReadHeaderTimeout,
			server.ReadTimeout,
			server.IdleTimeout,
		)
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(deadlineScalingListener{
			Listener: listener, readDuration: 25 * time.Millisecond,
		})
	}()
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("close slow-header server: %v", closeErr)
		}
		if runErr := <-serveErr; runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
			t.Errorf("serve slow-header server: %v", runErr)
		}
	})

	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			t.Errorf("close slow-header connection: %v", closeErr)
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: loopback"); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 256)
	count, err := connection.Read(buffer)
	if count == 0 && err != nil {
		if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
			t.Fatalf("slow headers remained open until the client deadline: %v", err)
		}
		if !errors.Is(err, io.EOF) {
			t.Fatalf("read slow-header refusal: %v", err)
		}
	}
	if got := string(buffer[:count]); count > 0 && !strings.HasPrefix(got, "HTTP/1.1 4") {
		t.Fatalf("slow-header response = %q, want an HTTP 4xx refusal", got)
	}
	select {
	case <-dispatched:
		t.Fatal("slow incomplete headers reached the handler")
	default:
	}
}

func TestMCPDaemonHandlerIsUnauthenticatedAndReportsSurface(t *testing.T) {
	handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
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
	handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
		Professor: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			dispatched++
			w.WriteHeader(http.StatusNoContent)
		}),
	})
	browser := httptest.NewRequest(http.MethodPost, config.MCPPathProfessor, http.NoBody)
	browser.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, browser)
	if response.Code != http.StatusForbidden || dispatched != 0 {
		t.Fatalf("browser-origin request status=%d dispatched=%d, want 403/0", response.Code, dispatched)
	}

	local := httptest.NewRequest(http.MethodPost, config.MCPPathProfessor, http.NoBody)
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

	professor, err := mcpserv.NewProfessor(mcpserv.ProfessorOptions{
		Version: "test", Chat: chat, Harvester: harvester,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
		Version: "test", StartedAt: time.Now(), Endpoint: "http://127.0.0.1:8377",
		Professor: professor.Handler(),
		Chat:      professor.FamilyHandler(config.MCPServerChat),
		Harvester: professor.FamilyHandler(config.MCPServerHarvester),
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := http.DefaultClient
	ctx := context.Background()
	for path, want := range map[string][]string{
		config.MCPPathProfessor:                         {"harvester_read", "chat_whoami", "servicedesk"},
		config.MCPFamilyPath(config.MCPServerChat):      {"chat_keys", "chat_whoami", "chat_self_compact", "chat_new"},
		config.MCPFamilyPath(config.MCPServerHarvester): {"harvester_read", "harvester_download_file"},
	} {
		session, err := connectHTTPMCP(t, ctx, server.URL+path, client)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		tools, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("%s tools/list: %v", path, err)
		}
		seen := map[string]bool{}
		for _, tool := range tools.Tools {
			seen[tool.Name] = true
		}
		for _, name := range want {
			if !seen[name] {
				t.Fatalf("%s tools omitted %q: %v", path, name, seen)
			}
		}
		if err := session.Close(); err != nil {
			t.Errorf("close %s session: %v", path, err)
		}
	}

	// Both families' tools answer over the one combined route.
	session, err := connectHTTPMCP(t, ctx, server.URL+config.MCPPathProfessor, client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	whoami, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "chat_whoami"})
	if err != nil {
		t.Fatalf("chat_whoami: %v", err)
	}
	if len(whoami.Content) == 0 {
		t.Fatalf("chat_whoami answered no content: %+v", whoami)
	}
	read, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "harvester_read", Arguments: map[string]any{"publications": []string{"doi:10.1000/never-match"}},
	})
	if err != nil {
		t.Fatalf("harvester_read: %v", err)
	}
	if len(read.Content) == 0 {
		t.Fatalf("harvester_read answered no content: %+v", read)
	}
}

// TestMCPDaemonDisabledRouteReturns503DistinctFromEnabledAndUnknownPath pins
// the #8 fix: mcp.servers.<name>.enabled=false must produce a response that
// cannot be mistaken for either a live server or a route that never existed.
// Before the fix, enabled=false and "fully live" were indistinguishable.
func TestMCPDaemonDisabledRouteReturns503DistinctFromEnabledAndUnknownPath(t *testing.T) {
	handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
		Chat: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		// Harvester left nil: mcp.servers.harvester.enabled=false never
		// constructs a handler for it to mount.
	})

	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, httptest.NewRequest(http.MethodPost, config.MCPFamilyPath("harvester"), http.NoBody))
	if disabled.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled route status = %d, want %d", disabled.Code, http.StatusServiceUnavailable)
	}
	wantBody := "pfm mcp: harvester is disabled by config; enable it with: pfm mcp harvester enable\n"
	if disabled.Body.String() != wantBody {
		t.Fatalf("disabled route body = %q, want %q", disabled.Body.String(), wantBody)
	}

	enabled := httptest.NewRecorder()
	handler.ServeHTTP(enabled, httptest.NewRequest(http.MethodPost, config.MCPFamilyPath("chat"), http.NoBody))
	if enabled.Code != http.StatusNoContent {
		t.Fatalf(
			"enabled route status = %d, want %d — disabling harvester must not dark chat",
			enabled.Code,
			http.StatusNoContent,
		)
	}

	chatOff := httptest.NewRecorder()
	mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
		Harvester: http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		),
	}).ServeHTTP(chatOff, httptest.NewRequest(http.MethodPost, config.MCPFamilyPath("chat"), http.NoBody))
	wantChatBody := "pfm mcp: chat is disabled by config; enable it with: pfm mcp chat enable\n"
	if chatOff.Code != http.StatusServiceUnavailable || chatOff.Body.String() != wantChatBody {
		t.Fatalf("disabled chat view = %d %q, want 503 %q", chatOff.Code, chatOff.Body.String(), wantChatBody)
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
			handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{Chat: test.chat, Harvester: test.harvester})
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
// tool list that claims `harvester_search_web` whether or not runtimeSearchEnabled
// holds. Before the fix, mcp_serve_command.go's package-level
// harvesterMCPTools always listed the web search tool; that defect is what this test
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
			professor, err := mcpserv.NewProfessor(mcpserv.ProfessorOptions{Version: "test", Harvester: harvester})
			if err != nil {
				t.Fatal(err)
			}
			handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
				Professor:      professor.Handler(),
				Harvester:      professor.FamilyHandler(config.MCPServerHarvester),
				HarvesterTools: harvester.ToolNames(),
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
				if name == "harvester_search_web" {
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
	server := &http.Server{Handler: mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
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

// TestMCPServeRefusesAPortHeldByAForeignService pins the other half of the
// single-instance gate: a port answering with something that is NOT pfm's
// status document must stop the start with a message naming the conflict.
// Before the fix the probe reported that case exactly as it reported "nothing
// is listening", so serve walked on to bind a port it could not have.
func TestMCPServeRefusesAPortHeldByAForeignService(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	foreign := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/html")
			if _, err := writer.Write([]byte("<html>not pfm</html>")); err != nil {
				t.Errorf("write foreign body: %v", err)
			}
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- foreign.Serve(listener) }()
	defer func() {
		if err := foreign.Close(); err != nil {
			t.Errorf("close foreign server: %v", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve foreign service: %v", err)
		}
	}()

	runtime := commandRuntime{Config: config.Defaults(t.TempDir(), nil)}
	runtime.Config.MCP.HTTP.Port = listener.Addr().(*net.TCPAddr).Port
	runtime.Config.MCPServers["chat"] = config.MCPServer{Enabled: true}
	var stdout, stderr bytes.Buffer
	if code := runMCPServe(&stdout, &stderr, runtime, nil); code != 1 {
		t.Fatalf("serve over a foreign service code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "already running") {
		t.Fatalf("a foreign service was reported as pfm's own daemon: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "held by something that is not pfm") {
		t.Fatalf("serve stderr = %q, want the port conflict named", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("serve printed a startup line for a port it never bound: %q", stdout.String())
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

// TestMCPStdioDegradesPerFamily pins the in-process stdio start when one
// enabled family fails to configure: the healthy family serves, and every tool
// of the failed family stays listed and answers an MCP error naming the
// family, the configuration error and the fix.
func TestMCPStdioDegradesPerFamily(t *testing.T) {
	for _, test := range []struct {
		name        string
		breakFamily func(*commandRuntime)
		failed      string
		failedCall  string
		failedArgs  map[string]any
		wantError   string
		healthyCall string
	}{
		{
			name: "harvester fails, chat serves",
			breakFamily: func(runtime *commandRuntime) {
				runtime.Config.Harvester.Fetch.ProxyURL = "http://bad host"
			},
			failed:      config.MCPServerHarvester,
			failedCall:  "harvester_read",
			failedArgs:  map[string]any{"urls": []string{"https://example.com/"}},
			wantError:   `parse Harvester proxy URL: parse "http://bad host": invalid character " " in host name`,
			healthyCall: "chat_ls",
		},
		{
			name:        "chat fails, harvester serves",
			breakFamily: func(runtime *commandRuntime) { runtime.Paths.TmuxDir = "" },
			failed:      config.MCPServerChat,
			failedCall:  "chat_ls",
			failedArgs:  map[string]any{},
			wantError:   "configure chat MCP backend: tmux directory is empty",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := jailTest(t)
			runtime := commandRuntime{Config: config.Defaults(root, nil), Paths: jailPaths(t)}
			runtime.Config.Path = root + "/config.json"
			runtime.Config.Harvester.Cache.Dir = root + "/cache"
			runtime.Config.MCPServers[config.MCPServerChat] = config.MCPServer{Enabled: true}
			runtime.Config.MCPServers[config.MCPServerHarvester] = config.MCPServer{Enabled: true}
			test.breakFamily(&runtime)

			var stderr bytes.Buffer
			professor, closeFamilies, err := newStdioProfessor(&stderr, runtime, mcpRuntime(runtime, true))
			exitCode := 0
			defer closeFamilies(&exitCode)
			if err != nil {
				t.Fatalf("newStdioProfessor error = %v, want a degraded start serving the healthy family", err)
			}
			if !strings.Contains(stderr.String(), test.failed) || !strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("stderr = %q, want one line naming %s and %q", stderr.String(), test.failed, test.wantError)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			serverTransport, clientTransport := mcp.NewInMemoryTransports()
			serverSession, err := professor.Server().Connect(ctx, serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = serverSession.Close() }()
			client := mcp.NewClient(&mcp.Implementation{Name: "pfm-test", Version: "test"}, nil)
			session, err := client.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()

			tools, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			listed := map[string]*mcp.Tool{}
			for _, tool := range tools.Tools {
				listed[tool.Name] = tool
			}
			want := append(mcpserv.ToolNames(), harvestmcp.RegisteredToolNames(harvestRuntime(runtime))...)
			for _, name := range want {
				if listed[name] == nil {
					t.Fatalf("tools/list lacks %s; listed %d tools", name, len(tools.Tools))
				}
			}
			if len(listed) != len(want) {
				t.Fatalf("tools/list holds %d tools, want %d", len(listed), len(want))
			}

			failed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: test.failedCall, Arguments: test.failedArgs})
			if err != nil {
				t.Fatalf("%s protocol error = %v, want an MCP error result", test.failedCall, err)
			}
			text := toolResultText(failed)
			if !failed.IsError {
				t.Fatalf("%s IsError = false, text %q", test.failedCall, text)
			}
			for _, part := range []string{
				test.failed + " family failed to configure", test.wantError, runtime.Config.Path, "/mcp",
			} {
				if !strings.Contains(text, part) {
					t.Fatalf("%s error text = %q, want it to hold %q", test.failedCall, text, part)
				}
			}

			if test.healthyCall != "" {
				healthy, err := session.CallTool(
					ctx,
					&mcp.CallToolParams{Name: test.healthyCall, Arguments: map[string]any{}},
				)
				if err != nil || healthy.IsError {
					t.Fatalf("%s = %q, %v; want a normal answer", test.healthyCall, toolResultText(healthy), err)
				}
				return
			}
			schema, err := json.Marshal(listed["harvester_read"].InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(schema), `"urls"`) {
				t.Fatalf("harvester_read schema = %s, want the healthy family's real schema", schema)
			}
		})
	}
}

func toolResultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}
