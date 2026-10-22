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
	if !strings.Contains(response.Body.String(), "chat") {
		t.Fatalf("status surface = %s", response.Body.String())
	}
}

func TestMCPDaemonRejectsBrowserOriginBeforeDispatch(t *testing.T) {
	dispatched := 0
	handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
		Chat: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			dispatched++
			w.WriteHeader(http.StatusNoContent)
		}),
	})
	browser := httptest.NewRequest(http.MethodPost, config.MCPFamilyPath(config.MCPServerChat), http.NoBody)
	browser.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, browser)
	if response.Code != http.StatusForbidden || dispatched != 0 {
		t.Fatalf("browser-origin request status=%d dispatched=%d, want 403/0", response.Code, dispatched)
	}

	local := httptest.NewRequest(http.MethodPost, config.MCPFamilyPath(config.MCPServerChat), http.NoBody)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, local)
	if response.Code != http.StatusNoContent || dispatched != 1 {
		t.Fatalf("origin-free local request status=%d dispatched=%d, want 204/1", response.Code, dispatched)
	}
}

func TestMCPDaemonMountedServersNeedNoAuthAndServeTools(t *testing.T) {
	jailTest(t)
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
	professor, err := mcpserv.NewFamilyServers(mcpserv.FamilyServerOptions{
		Version: "test", Chat: chat,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{
		Version: "test", StartedAt: time.Now(), Endpoint: "http://127.0.0.1:8377",
		Chat: professor.FamilyHandler(config.MCPServerChat),
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := http.DefaultClient
	ctx := context.Background()
	for path, want := range map[string][]string{
		config.MCPFamilyPath(config.MCPServerChat): {"keys", "whoami", "new"},
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

	// Chat tools answer through the mounted route.
	session, err := connectHTTPMCP(t, ctx, server.URL+config.MCPFamilyPath(config.MCPServerChat), client)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	whoami, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if len(whoami.Content) == 0 {
		t.Fatalf("whoami answered no content: %+v", whoami)
	}
}

// TestMCPDaemonDisabledRouteReturns503DistinctFromEnabledAndUnknownPath pins
// the #8 fix: mcp.servers.<name>.enabled=false must produce a response that
// cannot be mistaken for either a live server or a route that never existed.
// Before the fix, enabled=false and "fully live" were indistinguishable.
func TestMCPDaemonDisabledRouteReturns503DistinctFromEnabledAndUnknownPath(t *testing.T) {
	disabled := httptest.NewRecorder()
	mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{}).
		ServeHTTP(disabled, httptest.NewRequest(http.MethodPost, config.MCPFamilyPath("chat"), http.NoBody))
	if disabled.Code != http.StatusServiceUnavailable ||
		!strings.Contains(disabled.Body.String(), "pfm mcp chat enable") {
		t.Fatalf("disabled chat = %d %q", disabled.Code, disabled.Body.String())
	}
	handler := mcpserv.NewDaemonHandler(
		mcpserv.DaemonOptions{
			Chat: http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
			),
		},
	)
	enabled := httptest.NewRecorder()
	handler.ServeHTTP(enabled, httptest.NewRequest(http.MethodPost, config.MCPFamilyPath("chat"), http.NoBody))
	if enabled.Code != http.StatusNoContent {
		t.Fatalf("enabled chat status = %d", enabled.Code)
	}
	for _, path := range []string{
		"/mcp/unknown",
		config.MCPFamilyPath(config.MCPServerHarvester),
	} {
		unknown := httptest.NewRecorder()
		handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		if unknown.Code != http.StatusNotFound {
			t.Fatalf("unmounted route %s status = %d", path, unknown.Code)
		}
	}
}

// TestMCPDaemonStatusServersListsOnlyMountedHandlers pins the #8 fix to
// /status: the Servers surface must name only what was actually mounted,
// never a hardcoded pair regardless of config. This is the surface the
// original defect let lie.
func TestMCPDaemonStatusServersListsOnlyMountedHandlers(t *testing.T) {
	noop := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, test := range []struct {
		name string
		chat http.Handler
		want []string
	}{
		{"chat only mounted", noop, []string{"chat"}},
		{"chat disabled", nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := mcpserv.NewDaemonHandler(mcpserv.DaemonOptions{Chat: test.chat})
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

// TestMCPServeChatDisabledRefusesBeforeBindingPort pins the #8 fix at the
// command layer: mcp.servers.chat.enabled=false must refuse to start with a non-zero
// exit BEFORE the port is ever bound, not start an empty daemon that answers
// nothing.
func TestMCPServeChatDisabledRefusesBeforeBindingPort(t *testing.T) {
	// A different process can claim the probe's port after Close. A refusal
	// that bound the port will fail the rebind on every fresh attempt.
	for attempt := 0; attempt < 5; attempt++ {
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
		// Enabling the standalone registration never enables the chat daemon.
		runtime.Config.MCPServers[config.MCPServerHarvester] = config.MCPServer{Enabled: true}

		var stdout, stderr bytes.Buffer
		if code := runMCPServe(&stdout, &stderr, runtime, nil); code != 1 {
			t.Fatalf("both-disabled serve code = %d, want 1", code)
		}
		if stdout.Len() != 0 {
			t.Fatalf("both-disabled serve stdout = %q, want no startup line — nothing was mounted", stdout.String())
		}
		if !strings.Contains(stderr.String(), "chat is disabled by config") ||
			!strings.Contains(stderr.String(), "pfm mcp chat enable") {
			t.Fatalf(
				"both-disabled serve stderr = %q, want the disabled-config refusal and its enable hint",
				stderr.String(),
			)
		}

		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			continue
		}
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("both-disabled serve did not leave a freshly probed port bindable after 5 attempts")
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

func shortenMCPServeDaemonProbeTimeout(t *testing.T) {
	t.Helper()
	previous := mcpserv.DaemonProbeTimeoutOverride
	mcpserv.DaemonProbeTimeoutOverride = 100 * time.Millisecond
	t.Cleanup(func() { mcpserv.DaemonProbeTimeoutOverride = previous })
}

func TestMCPServeRefusesAPortHeldByASilentListener(t *testing.T) {
	shortenMCPServeDaemonProbeTimeout(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Errorf("close silent listener: %v", closeErr)
		}
	})
	port := listener.Addr().(*net.TCPAddr).Port
	runtime := commandRuntime{Config: config.Defaults(t.TempDir(), nil)}
	runtime.Config.MCP.HTTP.Port = port
	runtime.Config.MCPServers["chat"] = config.MCPServer{Enabled: true}
	var stdout, stderr bytes.Buffer
	if code := runMCPServe(&stdout, &stderr, runtime, nil); code != 1 {
		t.Fatalf("silent-listener serve code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	line := stderr.String()
	if !strings.Contains(
		line,
		"port "+strconv.Itoa(port)+" is held by a listener that did not answer pfm's status probe",
	) ||
		strings.Contains(line, "listen loopback") ||
		strings.Contains(line, "already running") {
		t.Fatalf("silent-listener serve stderr=%q, want named refusal before bind", line)
	}
	if stdout.Len() != 0 {
		t.Fatalf("silent-listener serve stdout=%q, want no startup line", stdout.String())
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
// A chat-only config whose chat family fails to configure still starts the
// server: every chat tool is listed and answers the configuration error.
func TestMCPStdioStartsWhenTheSoleFamilyFails(t *testing.T) {
	root := jailTest(t)
	runtime := commandRuntime{Config: config.Defaults(root, nil), Paths: jailPaths(t)}
	runtime.Config.Path = root + "/config.json"
	runtime.Config.MCPServers[config.MCPServerChat] = config.MCPServer{Enabled: true}
	runtime.Paths.TmuxDir = ""

	var stderr bytes.Buffer
	professor, closeFamilies, err := newStdioFamilies(&stderr, runtime, mcpRuntime(runtime, true))
	exitCode := 0
	defer closeFamilies(&exitCode)
	if err != nil {
		t.Fatalf("newStdioFamilies error = %v, want a started server whose chat tools answer the error", err)
	}
	if !strings.Contains(stderr.String(), "configure chat: ") {
		t.Fatalf("stderr = %q, want the line naming the failed chat family", stderr.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := professor.Server(config.MCPServerChat).Connect(ctx, serverTransport, nil)
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
	if len(tools.Tools) != len(mcpserv.ToolNames()) {
		t.Fatalf("tools/list holds %d tools, want the %d chat tools", len(tools.Tools), len(mcpserv.ToolNames()))
	}
	for _, name := range mcpserv.ToolNames() {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("%s protocol error = %v, want an MCP error result", name, err)
		}
		if text := toolResultText(result); !result.IsError ||
			!strings.Contains(text, "configure chat MCP backend: tmux directory is empty") {
			t.Fatalf("%s = IsError %t %q, want the configuration error", name, result.IsError, text)
		}
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
