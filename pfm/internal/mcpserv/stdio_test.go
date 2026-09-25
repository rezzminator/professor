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
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A client holds a stdio server's input open for the whole session, so a read
// on it never returns. Ending the context must still end RunStdio: the
// replaced-executable guard cancels the context and relies on the return to
// exit the process.
func TestRunStdioReturnsOnCancelWhileInputStaysOpen(t *testing.T) {
	professor := stdioTestProfessor(t, newIssuesTestService(t))
	input, holdOpen := io.Pipe()
	defer func() { _ = holdOpen.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- professor.RunStdio(ctx, input, io.Discard, StdioOptions{Warnings: io.Discard}) }()

	cancel()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("RunStdio did not return after its context ended while input stayed open")
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
		warnings: warnings, runtimeIdentity: "sha256:stdio-test",
		dispatch: func(_ context.Context, _ []string, stdout, _ io.Writer) int {
			_, _ = io.WriteString(stdout, marker+"\n")
			return 0
		},
	})
}

// stdioTestProfessor carries service as the professor server's chat family.
func stdioTestProfessor(t *testing.T, service *Service) *Professor {
	t.Helper()
	professor, err := NewProfessor(ProfessorOptions{Version: "test", Chat: service})
	if err != nil {
		t.Fatal(err)
	}
	return professor
}

func stdioTestOptions(service *Service, address string) StdioOptions {
	return StdioOptions{
		DaemonAddress: address, Home: service.backend.paths.Home,
		SIDDir: service.backend.paths.SIDDir, Warnings: service.backend.warnings,
	}
}

func stdioTestRun(t *testing.T, service *Service, address string) ActionOutput {
	return stdioTestRunInspect(t, service, address, nil)
}

func stdioTestRunInspect(t *testing.T, service *Service, address string, inspect func()) ActionOutput {
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
	professor := stdioTestProfessor(t, service)
	options := stdioTestOptions(service, address)
	go func() { returned <- professor.RunStdio(ctx, serverInput, serverOutput, options) }()
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
	address := proxyTestAddress(server)
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
	if marker := stdioTestRunInspect(t, local, address, inspect).Message; marker != "daemon" {
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

func TestRunStdioFallsBackWhenDaemonRuntimeDiffers(t *testing.T) {
	var routeRequests atomic.Int32
	daemon := proxyTestDaemon(stdioTestService("daemon", io.Discard))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/status" {
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(map[string]any{
				"pid": 1, "servers": map[string][]string{pfmconfig.MCPServerChat: ToolNames()},
				"chatRuntimeIdentity": "sha256:different",
			}); err != nil {
				t.Errorf("encode status: %v", err)
			}
			return
		}
		routeRequests.Add(1)
		daemon.ServeHTTP(writer, request)
	}))
	defer server.Close()

	var warnings bytes.Buffer
	local := stdioTestService("local", &warnings)
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	local.backend.paths = resolved
	address := proxyTestAddress(server)
	if marker := stdioTestRun(t, local, address).Message; marker != "local" {
		t.Fatalf("mismatched daemon marker = %q, want in-process local marker", marker)
	}
	if routeRequests.Load() != 0 {
		t.Fatalf("mismatched daemon received %d route requests, want none", routeRequests.Load())
	}
	assertNoProxyMarker(t, local)
	for _, part := range []string{"pfm mcp stdio: runtime mismatch with daemon at " + address + "; using in-process MCP; "} {
		if !strings.Contains(warnings.String(), part) {
			t.Errorf("mismatch warning %q does not name %q", warnings.String(), part)
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
	address := proxyTestAddress(server)
	err := stdioTestProfessor(t, local).RunStdio(
		context.Background(), io.NopCloser(bytes.NewReader(nil)), io.Discard, stdioTestOptions(local, address),
	)
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
	address := proxyTestAddress(selected)
	if marker := stdioTestRun(t, local, address).Message; marker != "selected" {
		t.Fatalf("selected daemon marker = %q, want selected (ambient daemon answered ambient)", marker)
	}
}

// TestRunStdioFallsBackWhenDaemonServesNoProfessorRoute pins the upgrade
// window: a daemon still running a build from before the professor server has
// a healthy /status with the same families and chat runtime, but answers 404
// on /mcp/professor. The stdio server serves in process rather than forwarding
// every call into that 404.
func TestRunStdioFallsBackWhenDaemonServesNoProfessorRoute(t *testing.T) {
	daemon := proxyTestDaemon(stdioTestService("pre-professor", io.Discard))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == pfmconfig.MCPPathProfessor {
			http.NotFound(writer, request)
			return
		}
		daemon.ServeHTTP(writer, request)
	}))
	defer server.Close()

	var warnings bytes.Buffer
	local := stdioTestService("local", &warnings)
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	local.backend.paths = resolved
	address := proxyTestAddress(server)
	if marker := stdioTestRun(t, local, address).Message; marker != "local" {
		t.Fatalf("pre-professor daemon marker = %q, want in-process local marker", marker)
	}
	assertNoProxyMarker(t, local)
	want := "pfm mcp stdio: daemon at " + address + " serves no " + pfmconfig.MCPPathProfessor
	if !strings.Contains(warnings.String(), want) {
		t.Errorf("pre-professor fallback warning %q does not name %q", warnings.String(), want)
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
	address := proxyTestAddress(server)
	if marker := stdioTestRun(t, local, address).Message; marker != "local" {
		t.Fatalf("unmounted chat marker = %q, want in-process local marker", marker)
	}
	if routeRequests.Load() != 0 {
		t.Fatalf("unmounted chat route received %d requests, want none", routeRequests.Load())
	}
	assertNoProxyMarker(t, local)
	for _, part := range []string{
		"pfm mcp stdio: family chat not mounted by daemon at " + address + "; using in-process MCP; ",
		"pfm install", "chat restarts",
	} {
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
	if marker := stdioTestRun(t, local, "").Message; marker != "local" {
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
	address := "127.0.0.1:" + strconv.Itoa(port)
	if marker := stdioTestRun(t, local, address).Message; marker != "local" {
		t.Fatalf("absent daemon marker = %q, want in-process local marker", marker)
	}
	line := warnings.String()
	assertNoProxyMarker(t, local)
	for _, part := range []string{
		"pfm mcp stdio: daemon absent at 127.0.0.1:" + strconv.Itoa(port) + " (",
		"no service answered", "; using in-process MCP; ", "pfm install", "chat restarts",
	} {
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
	address := proxyTestAddress(foreign)
	if marker := stdioTestRun(t, local, address).Message; marker != "local" {
		t.Fatalf("foreign daemon marker = %q, want in-process local marker", marker)
	}
	line := warnings.String()
	assertNoProxyMarker(t, local)
	for _, part := range []string{
		"pfm mcp stdio: foreign service at 127.0.0.1:" + strconv.Itoa(port) + " (",
		"HTTP 418", "pfm install", "chat restarts",
	} {
		if !strings.Contains(line, part) {
			t.Errorf("foreign fallback warning %q does not name %q", line, part)
		}
	}
	if strings.Contains(line, "daemon absent") {
		t.Fatalf("foreign fallback reused absent wording: %q", line)
	}
}

// stdioTestSession connects an MCP client to professor.RunStdio over pipes;
// cleanup closes the session and waits for RunStdio to return.
func stdioTestSession(t *testing.T, professor *Professor, options StdioOptions) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	serverInput, clientOutput := io.Pipe()
	clientInput, serverOutput := io.Pipe()
	returned := make(chan error, 1)
	go func() { returned <- professor.RunStdio(ctx, serverInput, serverOutput, options) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: clientInput, Writer: clientOutput}, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close stdio test session: %v", err)
		}
		cancel()
		select {
		case <-returned:
		case <-time.After(2 * time.Second):
			t.Error("RunStdio did not return after test client closed")
		}
	})
	return session
}

// stdioTestNamer answers every tmux session-name lookup with one name.
type stdioTestNamer string

func (namer stdioTestNamer) SessionName(context.Context, string, string) (string, error) {
	return string(namer), nil
}

func TestRunStdioForwardsHarvesterOnlyWithoutRuntimeIdentity(t *testing.T) {
	root := testjail.Fleet(t)
	daemonProfessor := newTestProfessor(t, ProfessorOptions{Harvester: newTestHarvester(t, harvestmcp.Runtime{})})
	roster := daemonProfessor.Servers()[pfmconfig.MCPServerHarvester]
	daemon := NewDaemonHandler(DaemonOptions{
		Version: "test", Endpoint: "test", Professor: daemonProfessor.Handler(),
		Harvester:      daemonProfessor.FamilyHandler(pfmconfig.MCPServerHarvester),
		HarvesterTools: roster,
	})
	var professorRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == pfmconfig.MCPPathProfessor {
			professorRequests.Add(1)
		}
		daemon.ServeHTTP(writer, request)
	}))
	defer server.Close()

	local := newTestProfessor(t, ProfessorOptions{Harvester: newTestHarvester(t, harvestmcp.Runtime{})})
	var warnings proxyTestBuffer
	session := stdioTestSession(t, local, StdioOptions{
		DaemonAddress: proxyTestAddress(server), Home: filepath.Join(root, "home"), Warnings: &warnings,
	})
	if names := sessionToolNames(t, session); !reflect.DeepEqual(names, sortedRoster(roster)) {
		t.Fatalf("forwarded tools/list = %v, want the daemon's harvester roster %v", names, sortedRoster(roster))
	}
	if professorRequests.Load() == 0 {
		t.Fatalf("daemon %s received no request; warnings: %s", pfmconfig.MCPPathProfessor, warnings.String())
	}
	if strings.Contains(warnings.String(), "using in-process MCP") {
		t.Fatalf("harvester-only daemon fell back in process: %s", warnings.String())
	}
}

// A Codex caller's _meta.threadId crosses the stdio forwarder unchanged, beside
// the pfmProxy identity the forwarder adds, and skips the Claude crumb refresh
// (whose relative breadcrumb directory here would refuse the call).
func TestRunStdioForwardsCodexCallerMetadataToProfessor(t *testing.T) {
	previous := proxyWhoami
	proxyWhoami = resolve.WhoamiDependencies{
		Environment: &resolve.WhoamiEnvironment{
			TMUX: "/tmp/tmux-1000/cc-seat,1,0", TMUXPane: "%7", ClaudeSessionID: "session-claude",
		},
		Namer: stdioTestNamer("cc-seat"),
	}
	t.Cleanup(func() { proxyWhoami = previous })

	daemon := proxyTestDaemon(stdioTestService("daemon", io.Discard))
	var mutex sync.Mutex
	var calls []map[string]any
	var callPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read daemon request: %v", err)
			}
			var frame map[string]any
			if json.Unmarshal(body, &frame) == nil && frame["method"] == "tools/call" {
				mutex.Lock()
				calls = append(calls, frame)
				callPaths = append(callPaths, request.URL.Path)
				mutex.Unlock()
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
		}
		daemon.ServeHTTP(writer, request)
	}))
	defer server.Close()

	var warnings proxyTestBuffer
	local := stdioTestService("local", &warnings)
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	session := stdioTestSession(t, stdioTestProfessor(t, local), StdioOptions{
		DaemonAddress: proxyTestAddress(server), Home: resolved.Home, SIDDir: "relative-sid", Warnings: &warnings,
	})
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: mcp.Meta{"threadId": "thread-codex"}, Name: "chat_ls", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(result.Content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "refresh Claude caller transcript") {
		t.Fatalf("Codex caller ran the Claude crumb refresh: %s", content)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(calls) != 1 || callPaths[0] != pfmconfig.MCPPathProfessor {
		t.Fatalf("daemon tools/call paths = %v, want one at %s; warnings: %s",
			callPaths, pfmconfig.MCPPathProfessor, warnings.String())
	}
	meta, _ := calls[0]["params"].(map[string]any)["_meta"].(map[string]any)
	if meta["threadId"] != "thread-codex" {
		t.Fatalf("forwarded _meta = %+v, want threadId thread-codex unchanged", meta)
	}
	identity, _ := meta["pfmProxy"].(map[string]any)
	if identity["session"] != "cc-seat" {
		t.Fatalf("forwarded _meta = %+v, want pfmProxy beside threadId", meta)
	}
}

// With no daemon, the in-process server resolves a Codex caller by its
// _meta.threadId, exactly as the daemon does.
func TestRunStdioResolvesCodexCallerInProcess(t *testing.T) {
	service := metadataIdentityService(t)
	probe := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := proxyTestAddress(probe)
	probe.Close()

	var warnings proxyTestBuffer
	session := stdioTestSession(t, stdioTestProfessor(t, service), StdioOptions{
		DaemonAddress: address, Home: service.backend.paths.Home,
		SIDDir: service.backend.paths.SIDDir, Warnings: &warnings,
	})
	whoami := callToolWithMeta[WhoamiOutput](
		t, session, "chat_whoami", mcp.Meta{"threadId": "thread-a"}, WhoamiInput{},
	)
	if whoami.Status != "ok" || whoami.ID != "thread-a" || whoami.Session != "fixture-session" ||
		whoami.Engine != string(pfmengine.Codex) {
		t.Fatalf("in-process whoami = %+v, want thread-a on fixture-session", whoami)
	}
	if !strings.Contains(warnings.String(), "daemon absent at "+address) {
		t.Fatalf("warnings = %q, want the absent-daemon fallback", warnings.String())
	}
}

// A stdio server whose chat family failed to configure cannot verify a
// daemon's chat runtime, so it never forwards: chat_ls answers the in-process
// configuration error and the daemon sees no chat call. A healthy chat whose
// runtime matches still forwards.
func TestRunStdioForwardsChatOnlyWhenLocalChatConfigured(t *testing.T) {
	for _, row := range []struct {
		name        string
		chatFailed  bool
		wantForward bool
	}{
		{name: "local chat failed", chatFailed: true, wantForward: false},
		{name: "local chat healthy and runtime matches", chatFailed: false, wantForward: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			testjail.Fleet(t)
			resolved, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			var daemonCalls [][]string
			daemonProfessor := newTestProfessor(t, ProfessorOptions{
				Chat:      proxyTestService("daemon", nil, &daemonCalls),
				Harvester: newTestHarvester(t, harvestmcp.Runtime{}),
			})
			daemon := NewDaemonHandler(DaemonOptions{
				Version: "test", Endpoint: "test", Professor: daemonProfessor.Handler(),
				Chat:                daemonProfessor.FamilyHandler(pfmconfig.MCPServerChat),
				Harvester:           daemonProfessor.FamilyHandler(pfmconfig.MCPServerHarvester),
				HarvesterTools:      daemonProfessor.Servers()[pfmconfig.MCPServerHarvester],
				ChatRuntimeIdentity: "sha256:proxy-test",
			})
			var chatRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Body != nil {
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Errorf("read daemon request body: %v", err)
					}
					if strings.Contains(string(body), `"chat_`) {
						chatRequests.Add(1)
					}
					request.Body = io.NopCloser(bytes.NewReader(body))
				}
				daemon.ServeHTTP(writer, request)
			}))
			defer server.Close()

			options := ProfessorOptions{Harvester: newTestHarvester(t, harvestmcp.Runtime{})}
			if row.chatFailed {
				options.Failed = []FailedFamily{{
					Family: pfmconfig.MCPServerChat, Tools: ToolNames(),
					Err: errors.New("chat config broken"), ConfigPath: "/pfm/config.toml",
				}}
			} else {
				var localCalls [][]string
				options.Chat = proxyTestService("local", nil, &localCalls)
			}
			var warnings proxyTestBuffer
			session := stdioTestSession(t, newTestProfessor(t, options), StdioOptions{
				DaemonAddress: proxyTestAddress(server), Home: resolved.Home,
				SIDDir: resolved.SIDDir, Warnings: &warnings,
			})
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "chat_ls", Arguments: map[string]any{},
			})
			if err != nil {
				t.Fatalf("chat_ls: %v; warnings: %s", err, warnings.String())
			}
			forwarded := chatRequests.Load()
			if !row.wantForward {
				if !result.IsError || len(result.Content) == 0 ||
					!strings.Contains(result.Content[0].(*mcp.TextContent).Text, "chat config broken") {
					t.Fatalf("chat_ls = %+v, want the in-process configuration error", result)
				}
				if forwarded != 0 {
					t.Fatalf("daemon saw %d chat requests, want 0; warnings: %s", forwarded, warnings.String())
				}
				return
			}
			if result.IsError {
				t.Fatalf("chat_ls = %+v, want the daemon's answer", result)
			}
			if forwarded == 0 {
				t.Fatalf("daemon saw no chat request; warnings: %s", warnings.String())
			}
		})
	}
}
