package mcpserv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A client holds a stdio server's input open for the whole session, so a read
// on it never returns. Ending the context must still end RunStdio: the
// replaced-executable guard cancels the context and relies on the return to
// exit the process.
func TestRunStdioReturnsOnCancelWhileInputStaysOpen(t *testing.T) {
	service := newIssuesTestService(t)
	input, holdOpen := io.Pipe()
	defer func() { _ = holdOpen.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.RunStdio(ctx, input, io.Discard) }()

	cancel()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("RunStdio did not return after its context ended while input stayed open")
	}
}

func TestNewConfiguredStoresSelectedDaemonAddress(t *testing.T) {
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigured("test", io.Discard, Runtime{
		Paths: resolved, DaemonAddress: "127.0.0.1:43117",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close configured service: %v", err)
		}
	}()
	if service.daemonAddress != "127.0.0.1:43117" {
		t.Fatalf("stored daemon address = %q, want selected runtime address", service.daemonAddress)
	}
}

func stdioTestConfigurePort(t *testing.T, port int) {
	t.Helper()
	root := testjail.Fleet(t)
	home := filepath.Join(root, "home")
	config := pfmconfig.Defaults(home, []string{filepath.Join(root, "claude")}, filepath.Join(root, "codex"))
	config.MCP.HTTP.Port = port
	content, err := pfmconfig.Marshal(config, false)
	if err != nil {
		t.Fatal(err)
	}
	path := pfmconfig.ResolvePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stdioTestPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, rawPort, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func stdioTestService(marker string, warnings io.Writer) *Service {
	return newService(marker, &backend{
		warnings: warnings,
		dispatch: func(_ context.Context, _ []string, stdout, _ io.Writer) int {
			_, _ = io.WriteString(stdout, marker+"\n")
			return 0
		},
	})
}

func stdioTestRun(t *testing.T, service *Service) ActionOutput {
	return stdioTestRunInspect(t, service, nil)
}

func stdioTestRunInspect(t *testing.T, service *Service, inspect func()) ActionOutput {
	t.Helper()
	if service.backend.paths.Home == "" {
		testjail.Fleet(t)
		resolved, err := paths.Resolve()
		if err != nil {
			t.Fatal(err)
		}
		service.backend.paths = resolved
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverInput, clientOutput := io.Pipe()
	clientInput, serverOutput := io.Pipe()
	returned := make(chan error, 1)
	go func() { returned <- service.RunStdio(ctx, serverInput, serverOutput) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspect != nil {
		inspect()
	}
	output := callTool[ActionOutput](
		t,
		session,
		"chat_new",
		NewInput{Name: "child", CWD: "/work/test"},
	)
	if err := session.Close(); err != nil {
		t.Errorf("close stdio test session: %v", err)
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("RunStdio did not return after test client closed")
	}
	return output
}

func TestRunStdioUsesHealthyDaemonProxy(t *testing.T) {
	var daemonWarnings, localWarnings bytes.Buffer
	daemon := stdioTestService("daemon", &daemonWarnings)
	server := httptest.NewServer(proxyTestDaemon(daemon))
	defer server.Close()

	local := stdioTestService("local", &localWarnings)
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	local.backend.paths = resolved
	local.daemonAddress = proxyTestAddress(server)
	markerPath := filepath.Join(local.backend.paths.Home, ".local", "state", "pfm", "mcp-proxy-compatible")
	inspect := func() {
		markerTarget, err := filepath.EvalSymlinks(markerPath)
		if err != nil {
			t.Fatal(err)
		}
		links, err := gather.NewProcFS("/proc").FDLinks(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range links {
			if link.Target == markerTarget {
				return
			}
		}
		t.Fatalf("healthy daemon proxy does not hold marker %s", markerPath)
	}
	if marker := stdioTestRunInspect(t, local, inspect).Message; marker != "daemon" {
		t.Fatalf("healthy daemon marker = %q, want proxy daemon marker", marker)
	}
	links, err := gather.NewProcFS("/proc").FDLinks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	markerTarget, err := filepath.EvalSymlinks(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if link.Target == markerTarget {
			t.Fatalf("proxy marker descriptor %d remained open after proxy returned", link.FD)
		}
	}
}

func TestRunStdioReturnsMarkerOpenFailureWithoutLocalFallback(t *testing.T) {
	var daemonWarnings bytes.Buffer
	daemon := stdioTestService("daemon", &daemonWarnings)
	server := httptest.NewServer(proxyTestDaemon(daemon))
	defer server.Close()

	root := testjail.Fleet(t)
	blockedHome := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blockedHome, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	local := stdioTestService("local", io.Discard)
	local.backend.paths.Home = blockedHome
	local.daemonAddress = proxyTestAddress(server)
	err := local.RunStdio(context.Background(), io.NopCloser(bytes.NewReader(nil)), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "protect selected daemon stdio proxy") ||
		!strings.Contains(err.Error(), "mcp-proxy-compatible") {
		t.Fatalf("err = %v, want contextual marker-open failure", err)
	}
}

func assertNoProxyMarker(t *testing.T, service *Service) {
	t.Helper()
	marker := filepath.Join(service.backend.paths.Home, ".local", "state", "pfm", "mcp-proxy-compatible")
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("in-process fallback marker %s stat = %v, want absent", marker, err)
	}
}

func TestRunStdioUsesSelectedDaemonAddress(t *testing.T) {
	var ambientWarnings, selectedWarnings bytes.Buffer
	ambient := httptest.NewServer(proxyTestDaemon(stdioTestService("ambient", &ambientWarnings)))
	defer ambient.Close()
	selected := httptest.NewServer(proxyTestDaemon(stdioTestService("selected", &selectedWarnings)))
	defer selected.Close()
	stdioTestConfigurePort(t, stdioTestPort(t, ambient))

	local := stdioTestService("local", io.Discard)
	local.daemonAddress = proxyTestAddress(selected)
	if marker := stdioTestRun(t, local).Message; marker != "selected" {
		t.Fatalf("selected daemon marker = %q, want selected (ambient daemon answered ambient)", marker)
	}
}

func TestRunStdioFallsBackWhenChatRouteIsNotMounted(t *testing.T) {
	daemon := proxyTestDaemon(stdioTestService("unmounted-route", io.Discard))
	var routeRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/status" {
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(DaemonStatus{PID: 1, Servers: map[string][]string{}}); err != nil {
				t.Errorf("encode status: %v", err)
			}
			return
		}
		routeRequests.Add(1)
		daemon.ServeHTTP(writer, request)
	}))
	defer server.Close()
	stdioTestConfigurePort(t, stdioTestPort(t, server))

	var warnings bytes.Buffer
	local := stdioTestService("local", &warnings)
	local.daemonAddress = proxyTestAddress(server)
	if marker := stdioTestRun(t, local).Message; marker != "local" {
		t.Fatalf("unmounted chat marker = %q, want in-process local marker", marker)
	}
	if routeRequests.Load() != 0 {
		t.Fatalf("unmounted chat route received %d requests, want none", routeRequests.Load())
	}
	assertNoProxyMarker(t, local)
	for _, part := range []string{"chat", "not mounted", local.daemonAddress, "pfm install", "chat restarts"} {
		if !strings.Contains(warnings.String(), part) {
			t.Errorf("unmounted fallback warning %q does not name %q", warnings.String(), part)
		}
	}
}

func TestRunStdioFallsBackWhenDaemonAddressIsMissing(t *testing.T) {
	ambient := httptest.NewServer(proxyTestDaemon(stdioTestService("ambient", io.Discard)))
	defer ambient.Close()
	stdioTestConfigurePort(t, stdioTestPort(t, ambient))

	var warnings bytes.Buffer
	local := stdioTestService("local", &warnings)
	if marker := stdioTestRun(t, local).Message; marker != "local" {
		t.Fatalf("missing-address marker = %q, want in-process local marker", marker)
	}
	if line := warnings.String(); !strings.Contains(line, "daemon address missing") ||
		!strings.Contains(line, "using in-process MCP") {
		t.Fatalf("missing-address warning = %q, want named local fallback", line)
	}
	assertNoProxyMarker(t, local)
}

func TestRunStdioNamesAbsentDaemonFallback(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	port := stdioTestPort(t, probe)
	probe.Close()
	stdioTestConfigurePort(t, port)

	var warnings bytes.Buffer
	local := stdioTestService("local", &warnings)
	local.daemonAddress = "127.0.0.1:" + strconv.Itoa(port)
	if marker := stdioTestRun(t, local).Message; marker != "local" {
		t.Fatalf("absent daemon marker = %q, want in-process local marker", marker)
	}
	line := warnings.String()
	assertNoProxyMarker(t, local)
	for _, part := range []string{"absent", "127.0.0.1:" + strconv.Itoa(port), "no service answered", "pfm install", "chat restarts"} {
		if !strings.Contains(line, part) {
			t.Errorf("absent fallback warning %q does not name %q", line, part)
		}
	}
}

func TestRunStdioNamesForeignDaemonFallbackDifferently(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "foreign", http.StatusTeapot)
	}))
	defer foreign.Close()
	port := stdioTestPort(t, foreign)
	stdioTestConfigurePort(t, port)

	var warnings bytes.Buffer
	local := stdioTestService("local", &warnings)
	local.daemonAddress = proxyTestAddress(foreign)
	if marker := stdioTestRun(t, local).Message; marker != "local" {
		t.Fatalf("foreign daemon marker = %q, want in-process local marker", marker)
	}
	line := warnings.String()
	assertNoProxyMarker(t, local)
	for _, part := range []string{"foreign", "127.0.0.1:" + strconv.Itoa(port), "HTTP 418", "pfm install", "chat restarts"} {
		if !strings.Contains(line, part) {
			t.Errorf("foreign fallback warning %q does not name %q", line, part)
		}
	}
	if strings.Contains(line, "daemon absent") {
		t.Fatalf("foreign fallback reused absent wording: %q", line)
	}
}
