package harvestpy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"testing"

	"hostops/pfm/internal/harvest"
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
	previous := browserWorkerCommand
	browserWorkerCommand = func(_, _ string) (*exec.Cmd, error) {
		command := exec.Command(os.Args[0], "-test.run=TestAskProtocolRoundTrip")
		command.Env = append(os.Environ(), "GO_HARVESTPY_FAKE_BROWSER_WORKER=1")
		return command, nil
	}
	defer func() { browserWorkerCommand = previous }()

	worker := NewBrowserWorker(Runtime{Python: "unused", Script: "unused"})
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

// TestNilAskHandlerFailsClosed pins that a missing SSRF handler refuses every
// ask rather than allowing Chrome to connect unvalidated.
func TestNilAskHandlerFailsClosed(t *testing.T) {
	if os.Getenv("GO_HARVESTPY_FAKE_BROWSER_WORKER") == "1" {
		fakeBrowserWorkerDenyingNothing()
		return
	}
	previous := browserWorkerCommand
	browserWorkerCommand = func(_, _ string) (*exec.Cmd, error) {
		command := exec.Command(os.Args[0], "-test.run=TestNilAskHandlerFailsClosed")
		command.Env = append(os.Environ(), "GO_HARVESTPY_FAKE_BROWSER_WORKER=1")
		return command, nil
	}
	defer func() { browserWorkerCommand = previous }()

	worker := NewBrowserWorker(Runtime{Python: "unused", Script: "unused"})
	html, _, err := worker.Fetch(context.Background(), "https://publisher.example.test/walled", "", true, 45000, nil)
	if err == nil {
		t.Fatal("nil onAsk must fail closed, got success")
	}
	if !strings.Contains(err.Error(), "no SSRF handler") {
		t.Fatalf("fail-closed reason not reported: %v", err)
	}
	_ = html
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
