package binwatch

import (
	"bytes"
	"context"
	"fmt"
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

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
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

// epoch is where every fake clock of this file starts.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// handClock is a clock whose watch ticker the test fires by hand. The ticker's
// channel is unbuffered, so a send returns only once the watcher took it: when
// the next send returns, the check that tick started has finished.
type handClock struct {
	*clock.Fake
	ticks chan time.Time
}

func newHandClock() *handClock {
	return &handClock{Fake: clock.NewFake(epoch), ticks: make(chan time.Time)}
}

func (hand *handClock) NewTicker(time.Duration) clock.Ticker { return handTicker{hand.ticks} }

type handTicker struct{ ticks chan time.Time }

func (ticker handTicker) C() <-chan time.Time { return ticker.ticks }
func (handTicker) Stop()                      {}

// tick fires one watch tick and returns once the watcher took it.
func (hand *handClock) tick(t *testing.T) {
	t.Helper()
	select {
	case hand.ticks <- epoch:
	case <-testwait.Context(t).Done():
		t.Fatal("the watcher never took a tick")
	}
}

// tickUntil fires ticks until replaced closes, the watcher's own report.
func (hand *handClock) tickUntil(t *testing.T, condition string, replaced <-chan struct{}) {
	t.Helper()
	for {
		select {
		case <-replaced:
			return
		case hand.ticks <- epoch:
		case <-testwait.Context(t).Done():
			t.Fatalf("%s: never reported", condition)
		}
	}
}

// open reports whether replaced is still open.
func open(replaced <-chan struct{}) bool {
	select {
	case <-replaced:
		return false
	default:
		return true
	}
}

// writeFakeBuild writes a build the watcher only stats: nothing starts it.
func writeFakeBuild(t *testing.T, path, body string) {
	t.Helper()
	if err := testjail.WriteExecutableNeverRun(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
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
	hand := newHandClock()
	replaced, err := watch(ctx, binary, watchTick, &bytes.Buffer{}, hand)
	if err != nil {
		t.Fatal(err)
	}
	hand.tick(t)
	hand.tick(t) // the first check has finished
	if !open(replaced) {
		t.Fatal("watcher fired with the binary untouched")
	}
	staged := filepath.Join(directory, "pfm.new")
	writeFakeBuild(t, staged, "new build")
	if err := os.Rename(staged, binary); err != nil {
		t.Fatal(err)
	}
	hand.tickUntil(t, "watcher to see the binary renamed over by a new build", replaced)
}

// TestWatchExecutableSeesACopyOverTheSamePath covers the install that keeps
// the inode: a copy onto the path changes size and mtime instead.
func TestWatchExecutableSeesACopyOverTheSamePath(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pfm")
	writeFakeBuild(t, binary, "old build")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hand := newHandClock()
	replaced, err := watch(ctx, binary, watchTick, &bytes.Buffer{}, hand)
	if err != nil {
		t.Fatal(err)
	}
	writeFakeBuild(t, binary, "a longer new build")
	hand.tickUntil(t, "watcher to see the binary copied over in place", replaced)
}

// TestWatchExecutableNeverRestartsOntoAMissingBinary: a path that vanishes
// mid-run is reported, not treated as a replacement — restarting onto nothing
// would take the daemon down with no build to come back on.
func TestWatchExecutableNeverRestartsOntoAMissingBinary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pfm")
	writeFakeBuild(t, binary, "build")
	stderr := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	hand := newHandClock()
	replaced, err := watch(ctx, binary, watchTick, stderr, hand)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		hand.tick(t) // the last send returns once the third check has finished
	}
	if !open(replaced) {
		t.Fatal("watcher fired on a missing binary")
	}
	cancel()
	if got, want := strings.ReplaceAll(
		stderr.String(),
		binary,
		"{binary}",
	), "pfm mcp serve: cannot stat own executable {binary} (stat {binary}: no such file or directory); still serving the build it started on\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestWatchExecutableRefusesAnUnreadableStart(t *testing.T) {
	if _, err := watch(
		context.Background(),
		filepath.Join(t.TempDir(), "absent"),
		watchTick,
		&bytes.Buffer{},
		clock.NewFake(epoch),
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
	// The default bounds, on a fake clock: no request is in flight at a
	// replacement, so the drain ends at once and arms no real timer.
	bounds := restartDefaults
	bounds.clock = clock.NewFake(epoch)
	go func() { code <- serveUntilReplaced(server, listener, replaced, &stderr, bounds) }()
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
	got, _ := testwait.Receive(t, "the daemon to exit after its binary was replaced", code)
	if got != ExitReplaced {
		t.Fatalf("exit code = %d, want %d", got, ExitReplaced)
	}
	if !strings.Contains(stderr.String(), "own executable was replaced") {
		t.Fatalf("stderr = %q, want the restart reason", stderr.String())
	}
}

// TestServeUntilReplacedCloseIsAPlainStop: an ordinary server close is not a
// replacement and must not claim one.
func TestServeUntilReplacedCloseIsAPlainStop(t *testing.T) {
	server, endpoint, code, stderr := startServe(t, nil)
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatalf("daemon not serving before the close: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	got, _ := testwait.Receive(t, "serve to return after Close", code)
	if got != 0 {
		t.Fatalf("plain close exit code = %d, want 0 (stderr %q)", got, stderr.String())
	}
}

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

type callOutcome struct {
	result *mcp.CallToolResult
	err    error
}

// TestServeRestartEndsStreamsAndFinishesInFlightCalls is the regression for the
// ~40 s restart gap: a connected MCP client's standalone GET stream never ends
// on its own, so the restart's Shutdown ran out its whole grace while the
// closed listener refused every reconnect. The restart must still answer a
// tools/call already in flight, then end that stream and exit with its
// drain and shutdown bounds unspent. The client keeps its default reconnect retries, as every
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
	// The drain runs on a fake clock the test steps one re-count at a time; the
	// bounds are far past anything a step reaches, so only the streams ending
	// can finish the restart.
	fake := clock.NewFake(epoch)
	bounds := restartBounds{drain: time.Hour, shutdown: time.Hour, clock: fake}
	replaced := make(chan struct{})
	endpoint, code, stderr := startRestartServe(t, handler, replaced, bounds)

	ctx, cancel := context.WithCancel(testwait.Context(t))
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
	testwait.Until(t, "the client to open its standalone GET stream", func() (bool, string) {
		open := openStreams.Load()
		return open > 0, fmt.Sprintf("open streams: %d", open)
	})
	outcome := make(chan callOutcome, 1)
	go func() {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "slow"})
		outcome <- callOutcome{result: result, err: err}
	}()
	testwait.Receive(t, "the tools/call to reach the tool", entered)

	close(replaced)
	testwait.Until(t, "the restart to park on its drain with the call still running", func() (bool, string) {
		return fake.Pending() == 2, fake.String()
	})
	close(release)

	var got int
	testwait.Until(t, "serve to exit once the call finished and the streams ended", func() (bool, string) {
		select {
		case got = <-code:
			return true, ""
		default:
		}
		fake.Advance(drainPoll) // one re-count of the requests still in flight
		return false, fake.String()
	})
	if got != ExitReplaced {
		t.Fatalf("exit code = %d, want %d (stderr %q)", got, ExitReplaced, stderr.String())
	}
	if strings.Contains(stderr.String(), "still running at the drain bound") {
		t.Fatalf("stderr = %q: the restart cut a call the drain should have let finish", stderr.String())
	}
	done, _ := testwait.Receive(t, "the in-flight tools/call to answer", outcome)
	if done.err != nil {
		t.Fatalf("in-flight tools/call failed across the restart: %v", done.err)
	}
	if len(done.result.Content) != 1 {
		t.Fatalf("in-flight tools/call content = %#v, want the tool's answer", done.result.Content)
	}
	if text, ok := done.result.Content[0].(*mcp.TextContent); !ok || text.Text != "finished" {
		t.Fatalf("in-flight tools/call answered %#v, want \"finished\"", done.result.Content[0])
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
	fake := clock.NewFake(epoch)
	bounds := restartBounds{drain: 300 * time.Millisecond, shutdown: time.Hour, clock: fake}
	replaced := make(chan struct{})
	endpoint, code, stderr := startRestartServe(t, handler, replaced, bounds)
	go func() {
		response, err := http.Post(endpoint, "application/json", strings.NewReader("{}"))
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	testwait.Receive(t, "the request to reach the handler", entered)
	close(replaced)
	testwait.Until(t, "the restart to park on its drain bound", func() (bool, string) {
		return fake.Pending() == 2, fake.String()
	})
	fake.Advance(bounds.drain) // exactly the drain bound: the shutdown bound is an hour away
	got, _ := testwait.Receive(t, "serve to exit once the drain bound cut the request", code)
	if got != ExitReplaced {
		t.Fatalf("exit code = %d, want %d", got, ExitReplaced)
	}
	if !strings.Contains(stderr.String(), "still running at the drain bound") {
		t.Fatalf("stderr = %q, want the cut request said out loud", stderr.String())
	}
}

// TestImageReplaced is the door every long-lived pfm process asks: an
// untouched executable is current, a rename or a copy over it is a
// replacement, and a vanished one is an error, never a replacement.
func TestImageReplaced(t *testing.T) {
	for _, tc := range []struct {
		name    string
		install func(t *testing.T, binary string)
		want    bool
		wantErr string
	}{
		{name: "untouched executable is current", install: func(*testing.T, string) {}},
		{name: "rename over the path is a replacement", want: true, install: func(t *testing.T, binary string) {
			staged := binary + ".new"
			writeFakeBuild(t, staged, "new build")
			if err := os.Rename(staged, binary); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "copy over the path is a replacement", want: true, install: func(t *testing.T, binary string) {
			writeFakeBuild(t, binary, "a longer new build")
		}},
		{name: "vanished executable is an error", wantErr: "no such file or directory", install: func(t *testing.T, binary string) {
			if err := os.Remove(binary); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "pfm")
			writeFakeBuild(t, binary, "old build")
			image, err := ImageAt(binary)
			if err != nil {
				t.Fatal(err)
			}
			tc.install(t, binary)
			got, err := image.Replaced()
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			wantErr := ""
			if tc.wantErr != "" {
				wantErr = "stat " + binary + ": " + tc.wantErr
			}
			if got != tc.want || gotErr != wantErr || image.Path != binary {
				t.Fatalf(
					"Replaced() = %t, %q, path %q; want %t, %q, path %q",
					got,
					gotErr,
					image.Path,
					tc.want,
					wantErr,
					binary,
				)
			}
		})
	}
}

func TestImageAtRefusesAnAbsentExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	_, err := ImageAt(path)
	want := "stat own executable " + path + ": stat " + path + ": no such file or directory"
	if err == nil || err.Error() != want {
		t.Fatalf("ImageAt error = %v, want %q", err, want)
	}
}
