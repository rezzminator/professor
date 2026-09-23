package harvestpy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestAskProtocolRoundTrip pins the interactive stdio protocol: a fake worker
// emits two guard asks — one public, one 169.254.169.254 — then a final
// response. AssertFetchable must be consulted for BOTH; the private one must
// be answered allow:false; the final response must still parse. No real
// process tree beyond the test binary itself.
func TestAskProtocolRoundTrip(t *testing.T) {
	if os.Getenv("GO_HARVESTPY_FAKE_BROWSER_WORKER") == "1" {
		fakeBrowserWorker()
		return
	}
	worker := NewBrowserWorker(Runtime{
		Python: "fake-browser",
		Script: "script",
		Runner: browserTestRunner(
			t,
			[]string{"https://publisher.example.test/walled", "http://169.254.169.254/latest/meta-data/"},
			"rendered",
		),
	})
	var consulted []string
	html, status, err := worker.Fetch(
		context.Background(),
		"https://publisher.example.test/walled",
		"",
		true,
		45000,
		func(url string) error {
			consulted = append(consulted, url)
			return harvest.AssertFetchable(url)
		},
	)
	if err != nil {
		t.Fatalf("interactive fetch failed: %v", err)
	}
	if status != 200 || !strings.Contains(html, "rendered") {
		t.Fatalf("final response misparsed: html=%q status=%d", html, status)
	}
	if len(consulted) != 2 {
		t.Fatalf("AssertFetchable consulted %d time(s), want 2: %q", len(consulted), consulted)
	}
	if !strings.Contains(html, "headless=true") {
		t.Fatalf("the requested headless mode did not reach the worker: %q", html)
	}
	if !strings.Contains(html, `allow=true reason=""`) {
		t.Fatalf("public ask was not allowed: %q", html)
	}
	if !strings.Contains(html, `allow=false reason="refusing private/internal host 169.254.169.254"`) {
		t.Fatalf("private ask was not denied by AssertFetchable: %q", html)
	}
}

// fakeBrowserWorker runs inside the re-executed test binary and speaks the
// worker side of the protocol: two asks, then one final line. The replies Go
// wrote are echoed back embedded in the final HTML so the parent can assert
// exactly what was answered.
func fakeBrowserWorker() {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake worker read request:", err)
		os.Exit(1)
	}
	var request struct {
		Op       string `json:"op"`
		URL      string `json:"url"`
		Headless *bool  `json:"headless"`
	}
	if err := json.Unmarshal([]byte(line), &request); err != nil || request.Op != "fetch" {
		fmt.Fprintf(os.Stderr, "fake worker bad request %q (err=%v)\n", line, err)
		os.Exit(1)
	}
	reply1 := askAndRead(reader, "https://publisher.example.test/walled")
	reply2 := askAndRead(reader, "http://169.254.169.254/latest/meta-data/")
	final := map[string]any{
		"ok": true,
		"html": fmt.Sprintf(
			"<html>rendered %s %s headless=%s</html>",
			reply1,
			reply2,
			headlessField(request.Headless),
		),
		"status": 200,
	}
	body, _ := json.Marshal(final)
	fmt.Println(string(body))
}

// headlessField renders the request's headless flag, "absent" when the Go
// side omitted it — the worker would then fall back to its own default.
func headlessField(value *bool) string {
	if value == nil {
		return "absent"
	}
	return fmt.Sprint(*value)
}

func askAndRead(reader *bufio.Reader, url string) string {
	request, _ := json.Marshal(map[string]string{"ask": "fetchable", "url": url})
	fmt.Println(string(request))
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake worker read reply:", err)
		os.Exit(1)
	}
	var reply AskReply
	if err := json.Unmarshal([]byte(line), &reply); err != nil {
		fmt.Fprintln(os.Stderr, "fake worker bad reply:", err)
		os.Exit(1)
	}
	return fmt.Sprintf("allow=%t reason=%q", reply.Allow, reply.Reason)
}

// TestBrowserFetchRequestCarriesTheGoOwnedDial is the Go half of L2-F7: the
// worker refuses to launch Chrome without the proxy Go owns (browser.py
// PROXY_REQUIRED), so the proxy and the validated resolver pin must BOTH reach
// it verbatim on every fetch — a dropped field there is an unpinned browser.
func TestBrowserFetchRequestCarriesTheGoOwnedDial(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-browser"}, deps.InteractiveScript{
		Pid: 7004, Stdin: requestWriter, Stdout: responseReader,
	})
	sent := make(chan BrowserFetchRequest, 1)
	go func() {
		line, err := bufio.NewReader(requestReader).ReadString('\n')
		if err != nil {
			return
		}
		var request browserWorkerRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			return
		}
		sent <- request.BrowserFetchRequest
		_, _ = fmt.Fprintln(
			responseWriter,
			`{"ok":true,"status":200,"html":"rendered","final_url":"https://publisher.example.test/landed"}`,
		)
	}()
	t.Cleanup(func() {
		_ = requestReader.Close()
		_ = responseWriter.Close()
	})
	worker := NewBrowserWorker(Runtime{Python: "fake-browser", Script: "script", Runner: runner})
	t.Cleanup(func() { _ = worker.Close() })
	_, _, finalURL, err := worker.FetchPinned(
		context.Background(),
		"https://publisher.example.test/walled",
		"http://127.0.0.1:8431",
		"MAP publisher.example.test 93.184.216.34",
		"https://www.google.com/",
		"t0k",
		nil,
		"",
		true,
		true,
		45000,
		func(string) error { return nil },
	)
	if err != nil {
		t.Fatalf("FetchPinned() error = %v", err)
	}
	if finalURL != "https://publisher.example.test/landed" {
		t.Fatalf("the address the render landed on did not come back: %q", finalURL)
	}
	select {
	case request := <-sent:
		if request.Proxy != "http://127.0.0.1:8431" {
			t.Fatalf("the Go-owned proxy did not reach the worker: %+v", request)
		}
		if request.HostResolverRules != "MAP publisher.example.test 93.184.216.34" {
			t.Fatalf("the validated resolver pin did not reach the worker: %+v", request)
		}
		if request.Referer != "https://www.google.com/" {
			t.Fatalf("the provenance Referer did not reach the worker: %+v", request)
		}
		if !request.PressLoaders {
			t.Fatalf("the registered site's press_loaders did not reach the worker: %+v", request)
		}
		if request.MarkerToken != "t0k" {
			t.Fatalf("the lazy-load marker token did not reach the worker: %+v", request)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never received a fetch request")
	}
}

// TestBrowserFetchRequestOmitsAFalsePressLoaders: a render Go did not ask to
// press carries no press_loaders key at all, so the worker's default (read-only
// scrolling) holds.
func TestBrowserFetchRequestOmitsAFalsePressLoaders(t *testing.T) {
	body, err := json.Marshal(browserWorkerRequest{
		Op:                  "fetch",
		BrowserFetchRequest: BrowserFetchRequest{URL: "https://forum.example.test/t/1", PressLoaders: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "press_loaders") {
		t.Fatalf("a false press_loaders reached the wire: %s", body)
	}
	body, err = json.Marshal(browserWorkerRequest{
		Op:                  "fetch",
		BrowserFetchRequest: BrowserFetchRequest{URL: "https://www.reddit.com/r/x/comments/1/", PressLoaders: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"press_loaders":true`) {
		t.Fatalf("a true press_loaders is missing from the wire: %s", body)
	}
}

// TestNilAskHandlerFailsClosed pins that a missing SSRF handler refuses every
// ask rather than allowing Chrome to connect unvalidated.
func TestNilAskHandlerFailsClosed(t *testing.T) {
	if os.Getenv("GO_HARVESTPY_FAKE_BROWSER_WORKER") == "1" {
		fakeBrowserWorkerDenyingNothing()
		return
	}
	worker := NewBrowserWorker(Runtime{
		Python: "fake-browser", Script: "script",
		Runner: browserTestRunner(t, []string{"https://example.test/"}, "denied"),
	})
	html, _, err := worker.Fetch(context.Background(), "https://publisher.example.test/walled", "", true, 45000, nil)
	if err == nil {
		t.Fatal("nil onAsk must fail closed, got success")
	}
	if !strings.Contains(err.Error(), "no SSRF handler") {
		t.Fatalf("fail-closed reason not reported: %v", err)
	}
	_ = html
}

func browserTestRunner(t *testing.T, urls []string, mode string) *deps.FakeRunner {
	t.Helper()
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-browser"}, deps.InteractiveScript{
		Pid:    7001,
		Stdin:  requestWriter,
		Stdout: responseReader,
	})
	go func() {
		reader := bufio.NewReader(requestReader)
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var request struct {
			Op       string `json:"op"`
			URL      string `json:"url"`
			Headless *bool  `json:"headless"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			return
		}
		replies := make([]AskReply, 0, len(urls))
		for _, rawURL := range urls {
			ask, _ := json.Marshal(map[string]string{"ask": "fetchable", "url": rawURL})
			if _, err := fmt.Fprintln(responseWriter, string(ask)); err != nil {
				return
			}
			replyLine, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			var reply AskReply
			if json.Unmarshal([]byte(replyLine), &reply) != nil {
				return
			}
			replies = append(replies, reply)
		}
		body := map[string]any{"ok": true, "status": 200, "html": mode}
		if mode == "denied" {
			body = map[string]any{
				"ok":    false,
				"error": fmt.Sprintf("handler said allow=%t reason=%s", replies[0].Allow, replies[0].Reason),
			}
		} else {
			body["html"] = fmt.Sprintf(
				"<html>rendered headless=%t allow=%t reason=%q allow=%t reason=%q</html>",
				*request.Headless,
				replies[0].Allow,
				replies[0].Reason,
				replies[1].Allow,
				replies[1].Reason,
			)
		}
		encoded, _ := json.Marshal(body)
		_, _ = fmt.Fprintln(responseWriter, string(encoded))
		_ = responseWriter.Close()
	}()
	t.Cleanup(func() {
		_ = requestReader.Close()
		_ = requestWriter.Close()
		_ = responseReader.Close()
		_ = responseWriter.Close()
	})
	return runner
}

func TestBrowserWorkerCancellationClosesPendingRead(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	pending := newBlockingReadCloser()
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-browser"}, deps.InteractiveScript{
		Pid:    7002,
		Stdin:  requestWriter,
		Stdout: pending,
	})
	go func() {
		_, _ = io.Copy(io.Discard, requestReader)
	}()
	worker := NewBrowserWorker(Runtime{Python: "fake-browser", Script: "script", Runner: runner})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := worker.Fetch(ctx, "https://example.test/", "", true, 1000, nil)
		result <- err
	}()
	select {
	case <-pending.started:
	case <-time.After(time.Second):
		t.Fatal("browser worker did not reach its pending response read")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "request cancelled") {
			t.Fatalf("Fetch() error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Fetch() remained blocked after cancellation")
	}
	select {
	case <-pending.closed:
	case <-time.After(time.Second):
		t.Fatal("pending browser response read survived cancellation")
	}
	_ = requestReader.Close()
}

func TestBrowserWorkerFallsBackToDirectKillWhenGroupKillFails(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	defer func() {
		_ = stdinReader.Close()
		_ = stdoutWriter.Close()
	}()

	fake := &deps.FakeRunner{}
	fake.ScriptInteractive([]string{"fake-browser"}, deps.InteractiveScript{
		Pid:      7003,
		Stdin:    stdinWriter,
		Stdout:   stdoutReader,
		GroupErr: errors.New("group kill denied"),
	})
	worker := NewBrowserWorker(Runtime{Python: "fake-browser", Script: "script", Runner: fake})
	if _, err := worker.ensureWorkerLocked(); err != nil {
		t.Fatalf("ensureWorkerLocked() error = %v", err)
	}
	if err := worker.Close(); err == nil || !strings.Contains(err.Error(), "kill browser worker process group") {
		t.Fatalf("Close() error = %v, want contextual group-kill error", err)
	}
	calls := fake.LifecycleCalls()
	if len(calls) != 3 || calls[0].Action != "kill-group" || calls[1].Action != "kill" || calls[2].Action != "wait" {
		t.Fatalf("lifecycle calls = %+v, want kill-group, kill, wait", calls)
	}
}

type blockingReadCloser struct {
	started chan struct{}
	closed  chan struct{}
	start   sync.Once
	close   sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (reader *blockingReadCloser) Read([]byte) (int, error) {
	reader.start.Do(func() { close(reader.started) })
	<-reader.closed
	return 0, io.ErrClosedPipe
}

func (reader *blockingReadCloser) Close() error {
	reader.close.Do(func() { close(reader.closed) })
	return nil
}

func fakeBrowserWorkerDenyingNothing() {
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')
	request, _ := json.Marshal(map[string]string{"ask": "fetchable", "url": "https://example.test/"})
	fmt.Println(string(request))
	line, err := reader.ReadString('\n')
	if err != nil {
		os.Exit(1)
	}
	var reply AskReply
	_ = json.Unmarshal([]byte(line), &reply)
	body, _ := json.Marshal(
		map[string]any{"ok": false, "error": fmt.Sprintf("handler said allow=%t reason=%s", reply.Allow, reply.Reason)},
	)
	fmt.Println(string(body))
}

// TestBrowserRouteGuardPythonSeam runs the reference-extracted route-guard
// seam test with NO browser and NO patchright: a denied ask must abort the
// request, never continue it (the R4 CRITICAL).
func TestBrowserRouteGuardPythonSeam(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("named gap: python3 is unavailable on this host; the browser route-guard seam test did not run")
	}
	script := filepath.Join("assets", "browser", "browser_route_guard_test.py")
	command := exec.Command(python, script)
	command.Dir = assetDirForTest()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser route-guard seam failed: %v\n%s", err, output)
	}
}

// TestBrowserRenderPythonSeam runs the render seam test with NO browser and
// NO patchright: the rung sends a stock Chrome User-Agent (never
// HeadlessChrome) and the provenance Referer, and scrolls a lazy-loaded page
// until it stops growing — bounded by a round and a time cap, with a capped
// render still growing stamped incomplete.
func TestBrowserRenderPythonSeam(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("named gap: python3 is unavailable on this host; the browser render seam test did not run")
	}
	command := exec.Command(python, filepath.Join("assets", "browser", "browser_render_test.py"))
	command.Dir = assetDirForTest()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser render seam failed: %v\n%s", err, output)
	}
}

// TestBrowserConsentPythonSeam runs the consent seam's pure cases with NO
// browser and NO patchright: only a privacy-preserving label is ever pressed,
// and a scroll that did not move is unblocked once, then stopped "blocked" and
// stamped incomplete — never "stable".
func TestBrowserConsentPythonSeam(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("named gap: python3 is unavailable on this host; the browser consent seam test did not run")
	}
	command := exec.Command(python, filepath.Join("assets", "browser", "browser_consent_test.py"))
	command.Dir = assetDirForTest()
	command.Env = append(os.Environ(), "BROWSER_LIVE=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser consent seam failed: %v\n%s", err, output)
	}
}

func assetDirForTest() string {
	if _, err := os.Stat(filepath.Join("assets", "browser")); err == nil {
		return "."
	}
	return filepath.Join("internal", "harvestpy", "assets")
}

// TestLiveBrowserWorkerFetch exercises the real Patchright + system-Chrome
// path end to end. It is OPTIONAL by design and skips with a named reason on
// any host that has not opted in (HARVESTER_BROWSER), never provisioned the
// environment, or lacks a Chrome binary.
func liveBrowserRuntime(root string) Runtime {
	platform := Platform{GOOS: goRuntime.GOOS, GOARCH: goRuntime.GOARCH}
	current := BrowserRuntimeRoot(root, platform)
	return Runtime{
		Python: filepath.Join(current, "project", ".venv", "bin", "python"),
		Script: filepath.Join(current, "project", "browser.py"),
	}
}
