package binwatch

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// lockedBuffer is a stderr the watcher goroutine and the test can share.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (locked *lockedBuffer) Write(data []byte) (int, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.Write(data)
}

func (locked *lockedBuffer) String() string {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.String()
}

const watchTick = 10 * time.Millisecond

func writeFakeBuild(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitReplaced(replaced <-chan struct{}, within time.Duration) bool {
	select {
	case <-replaced:
		return true
	case <-time.After(within):
		return false
	}
}

// TestWatchExecutableSeesAnAtomicInstall is the regression for the stale
// shared daemon: make host-install renames a new build over ~/.local/bin/pfm,
// and the daemon — the one pfm process nobody closes — kept serving the old
// image for days. The watcher must fire on exactly that rename.
func TestWatchExecutableSeesAnAtomicInstall(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "pfm")
	writeFakeBuild(t, binary, "old build")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replaced, err := watch(ctx, binary, watchTick, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if waitReplaced(replaced, 20*watchTick) {
		t.Fatal("watcher fired with the binary untouched")
	}
	staged := filepath.Join(directory, "pfm.new")
	writeFakeBuild(t, staged, "new build")
	if err := os.Rename(staged, binary); err != nil {
		t.Fatal(err)
	}
	if !waitReplaced(replaced, 2*time.Second) {
		t.Fatal("watcher never saw the binary renamed over by a new build")
	}
}

// TestWatchExecutableSeesACopyOverTheSamePath covers the install that keeps
// the inode: a copy onto the path changes size and mtime instead.
func TestWatchExecutableSeesACopyOverTheSamePath(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pfm")
	writeFakeBuild(t, binary, "old build")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replaced, err := watch(ctx, binary, watchTick, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	writeFakeBuild(t, binary, "a longer new build")
	if !waitReplaced(replaced, 2*time.Second) {
		t.Fatal("watcher never saw the binary copied over in place")
	}
}

// TestWatchExecutableNeverRestartsOntoAMissingBinary: a path that vanishes
// mid-run is reported, not treated as a replacement — restarting onto nothing
// would take the daemon down with no build to come back on.
func TestWatchExecutableNeverRestartsOntoAMissingBinary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pfm")
	writeFakeBuild(t, binary, "build")
	stderr := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	replaced, err := watch(ctx, binary, watchTick, stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if waitReplaced(replaced, 20*watchTick) {
		t.Fatal("watcher fired on a missing binary")
	}
	cancel()
	time.Sleep(2 * watchTick)
	if got := stderr.String(); strings.Count(got, "cannot stat own executable") != 1 {
		t.Fatalf("stderr = %q, want the unreadable executable said once", got)
	}
}

func TestWatchExecutableRefusesAnUnreadableStart(t *testing.T) {
	if _, err := watch(
		context.Background(),
		filepath.Join(t.TempDir(), "absent"),
		watchTick,
		&bytes.Buffer{},
	); err == nil {
		t.Fatal("watching an absent executable returned no error")
	}
}

func startServe(t *testing.T, replaced <-chan struct{}) (*http.Server, string, <-chan int, *bytes.Buffer) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})}
	var stderr bytes.Buffer
	code := make(chan int, 1)
	go func() { code <- serveUntilReplaced(server, listener, replaced, &stderr, restartDefaults) }()
	return server, "http://" + listener.Addr().String(), code, &stderr
}

// TestServeUntilReplacedExitsForTheSupervisor: a replacement stops the
// daemon with ExitReplaced, the non-zero status systemd's
// Restart=on-failure and launchd's KeepAlive both restart on.
func TestServeUntilReplacedExitsForTheSupervisor(t *testing.T) {
	replaced := make(chan struct{})
	_, endpoint, code, stderr := startServe(t, replaced)
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatalf("daemon not serving before the replacement: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	close(replaced)
	select {
	case got := <-code:
		if got != ExitReplaced {
			t.Fatalf("exit code = %d, want %d", got, ExitReplaced)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon kept serving after its binary was replaced")
	}
	if !strings.Contains(stderr.String(), "own executable was replaced") {
		t.Fatalf("stderr = %q, want the restart reason", stderr.String())
	}
}

// TestServeUntilReplacedCloseIsAPlainStop: an ordinary server close is not a
// replacement and must not claim one.
func TestServeUntilReplacedCloseIsAPlainStop(t *testing.T) {
	server, _, code, stderr := startServe(t, nil)
	time.Sleep(20 * time.Millisecond)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-code:
		if got != 0 {
			t.Fatalf("plain close exit code = %d, want 0 (stderr %q)", got, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after Close")
	}
}

// restartExitBound is how soon after a replacement the daemon must be gone
// when nothing but open streams and a short call hold it: the port refuses
// every reconnect until the supervisor relaunches, so the restart gap is this
// exit plus the relaunch, never the full shutdown grace.
const restartExitBound = 2 * time.Second

// startRestartServe runs serve over handler with short restart bounds and
// returns its endpoint and exit code channel.
func startRestartServe(
	t *testing.T,
	handler http.Handler,
	replaced <-chan struct{},
	bounds restartBounds,
) (string, <-chan int, *lockedBuffer) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stderr := &lockedBuffer{}
	code := make(chan int, 1)
	go func() { code <- serveUntilReplaced(&http.Server{Handler: handler}, listener, replaced, stderr, bounds) }()
	return "http://" + listener.Addr().String(), code, stderr
}

// awaitExit returns serve's exit code and how long after since it arrived,
// failing when it never arrives within limit.
func awaitExit(t *testing.T, code <-chan int, since time.Time, limit time.Duration) (int, time.Duration) {
	t.Helper()
	select {
	case got := <-code:
		return got, time.Since(since)
	case <-time.After(limit):
		t.Fatalf("serve still running %v after the replacement", limit)
		return 0, 0
	}
}

type callOutcome struct {
	result *mcp.CallToolResult
	err    error
}

// TestServeRestartEndsStreamsAndFinishesInFlightCalls is the regression for the
// ~40 s restart gap: a connected MCP client's standalone GET stream never ends
// on its own, so the restart's Shutdown ran out its whole grace while the
// closed listener refused every reconnect. The restart must still answer a
// tools/call already in flight, then end that stream and exit within
// restartExitBound. The client keeps its default reconnect retries, as every
// real MCP client does.
func TestServeRestartEndsStreamsAndFinishesInFlightCalls(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "binwatch-test", Version: "test"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "slow", Description: "answers once released"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			close(entered)
			<-release
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "finished"}}}, nil, nil
		})
	sdkHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{JSONResponse: true},
	)
	var openStreams atomic.Int32
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			openStreams.Add(1)
			defer openStreams.Add(-1)
		}
		sdkHandler.ServeHTTP(writer, request)
	})
	bounds := restartBounds{drain: 4 * time.Second, shutdown: 6 * time.Second}
	replaced := make(chan struct{})
	endpoint, code, stderr := startRestartServe(t, handler, replaced, bounds)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Logf("close session after the daemon exited: %v", err)
		}
	}()
	for deadline := time.Now().Add(5 * time.Second); openStreams.Load() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the client never opened its standalone GET stream")
		}
		time.Sleep(watchTick)
	}
	outcome := make(chan callOutcome, 1)
	go func() {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "slow"})
		outcome <- callOutcome{result: result, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the tools/call never reached the tool")
	}

	start := time.Now()
	close(replaced)
	time.Sleep(200 * time.Millisecond) // the call is still running when the restart begins
	close(release)

	got, elapsed := awaitExit(t, code, start, bounds.shutdown+5*time.Second)
	t.Logf("restart exit %v after the replacement", elapsed)
	if got != ExitReplaced {
		t.Fatalf("exit code = %d, want %d (stderr %q)", got, ExitReplaced, stderr.String())
	}
	if elapsed > restartExitBound {
		t.Fatalf(
			"restart exit took %v, want under %v: an open MCP stream held the shutdown (stderr %q)",
			elapsed, restartExitBound, stderr.String(),
		)
	}
	select {
	case done := <-outcome:
		if done.err != nil {
			t.Fatalf("in-flight tools/call failed across the restart: %v", done.err)
		}
		if len(done.result.Content) != 1 {
			t.Fatalf("in-flight tools/call content = %#v, want the tool's answer", done.result.Content)
		}
		if text, ok := done.result.Content[0].(*mcp.TextContent); !ok || text.Text != "finished" {
			t.Fatalf("in-flight tools/call answered %#v, want \"finished\"", done.result.Content[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight tools/call never answered")
	}
}

// TestServeRestartCutsAStuckRequestAtTheDrainBound: a request that never ends
// is cut at the drain bound and said out loud, instead of holding the restart
// to the shutdown grace.
func TestServeRestartCutsAStuckRequestAtTheDrainBound(t *testing.T) {
	entered := make(chan struct{})
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(entered)
		<-request.Context().Done()
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	bounds := restartBounds{drain: 300 * time.Millisecond, shutdown: 6 * time.Second}
	replaced := make(chan struct{})
	endpoint, code, stderr := startRestartServe(t, handler, replaced, bounds)
	go func() {
		response, err := http.Post(endpoint, "application/json", strings.NewReader("{}"))
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the handler")
	}
	start := time.Now()
	close(replaced)
	got, elapsed := awaitExit(t, code, start, bounds.shutdown+5*time.Second)
	t.Logf("restart exit %v after the replacement", elapsed)
	if got != ExitReplaced {
		t.Fatalf("exit code = %d, want %d", got, ExitReplaced)
	}
	if elapsed > bounds.drain+restartExitBound {
		t.Fatalf("restart exit took %v, want under drain %v + %v", elapsed, bounds.drain, restartExitBound)
	}
	if !strings.Contains(stderr.String(), "still running at the drain bound") {
		t.Fatalf("stderr = %q, want the cut request said out loud", stderr.String())
	}
}
