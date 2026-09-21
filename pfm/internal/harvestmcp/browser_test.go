package harvestmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
)

// fakeBrowserWorker is an in-memory stand-in for the Patchright worker: the
// stdio protocol and nothing else. A real Python process is never started and
// no Chrome is ever launched, so these tests say nothing about Chrome's own
// flags — they pin what GO sends.
type fakeBrowserWorker struct {
	runner   *deps.FakeRunner
	python   string
	script   string
	requests chan map[string]any
}

// newFakeBrowserWorker provisions a browser environment fixture (interpreter,
// script and a matching environment.json, so EnsureBrowser reuses it instead
// of provisioning) and wires a fake worker that records each request line and
// answers it. Both browser tests in this package share it rather than each
// carrying a copy of the 40-line fixture.
func newFakeBrowserWorker(t *testing.T) (pythonConverter, *fakeBrowserWorker) {
	t.Helper()
	root := t.TempDir()
	platform := harvestpy.Platform{GOOS: goRuntime.GOOS, GOARCH: goRuntime.GOARCH}
	current := harvestpy.BrowserRuntimeRoot(root, platform)
	python := filepath.Join(current, "project", ".venv", "bin", "python")
	script := filepath.Join(current, "project", "browser.py")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{python, script} {
		if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	record := harvestpy.EnvironmentDigest{
		Schema:       1,
		SourceSHA256: sha256Hex(harvestpy.BrowserWorkerSource()),
		LockSHA256:   sha256Hex(harvestpy.BrowserLockMetadata()),
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "environment.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{python}, deps.InteractiveScript{
		Pid:    9101,
		Stdin:  stdinWriter,
		Stdout: stdoutReader,
	})
	worker := &fakeBrowserWorker{runner: runner, python: python, script: script, requests: make(chan map[string]any, 4)}
	go func() {
		defer func() { _ = stdoutWriter.Close() }()
		line, readErr := bufio.NewReader(stdinReader).ReadString('\n')
		if readErr != nil {
			return
		}
		request := map[string]any{}
		if unmarshalErr := json.Unmarshal([]byte(line), &request); unmarshalErr != nil {
			// A request the fake cannot parse must not read as "no request":
			// record the failure so the assertion names it.
			request = map[string]any{"decode_error": unmarshalErr.Error(), "raw_len": len(line)}
		}
		worker.requests <- request
		_, _ = fmt.Fprintln(stdoutWriter, `{"ok":true,"html":"fake browser result","status":200}`)
	}()
	return pythonConverter{browserRoot: root, runner: runner}, worker
}

// request returns the one request the fake worker received, failing the test
// when none arrived — "the worker was never asked" is a different fact from
// "the worker was asked for the wrong thing", and only one of them is a
// missing proxy.
func (w *fakeBrowserWorker) request(t *testing.T) map[string]any {
	t.Helper()
	select {
	case request := <-w.requests:
		return request
	default:
		t.Fatal("the browser worker received no request at all")
		return nil
	}
}

// TestFetchBrowserSendsAGoOwnedPinnedProxy is L2-F7's Go half: browser.py
// refuses to launch Chrome without a proxy (PROXY_REQUIRED), because only a
// proxy Go owns makes the address Go validated the address Chrome connects
// to. Before this fix the Go side sent an empty proxy and the rung was dead.
func TestFetchBrowserSendsAGoOwnedPinnedProxy(t *testing.T) {
	converter, worker := newFakeBrowserWorker(t)
	html, status, err := converter.FetchBrowser(context.Background(), "https://93.184.216.34/f7", true)
	if err != nil {
		t.Fatalf("FetchBrowser() error = %v", err)
	}
	if html != "fake browser result" || status != 200 {
		t.Fatalf("FetchBrowser() = (%q, %d), want the fake response", html, status)
	}
	request := worker.request(t)
	proxy, _ := request["proxy"].(string)
	if proxy == "" {
		t.Fatalf("browser fetch carried no proxy: %v — browser.py refuses to launch without one", request)
	}
	if !strings.HasPrefix(proxy, "http://127.0.0.1:") {
		t.Fatalf("browser proxy = %q, want the Go-owned loopback proxy", proxy)
	}
}

// TestFetchBrowserKeepsTheOperatorsOwnProxy is the other arm: when the
// operator configured fetch.proxyURL, every other rung in this harvester
// already leaves the dial to that proxy (harvest.Options.ProxyURL), and the
// browser rung must not quietly substitute its own.
func TestFetchBrowserKeepsTheOperatorsOwnProxy(t *testing.T) {
	converter, worker := newFakeBrowserWorker(t)
	converter.proxyURL = "http://proxy.example.test:8080"
	if _, _, err := converter.FetchBrowser(context.Background(), "https://93.184.216.34/f7", true); err != nil {
		t.Fatalf("FetchBrowser() error = %v", err)
	}
	request := worker.request(t)
	if proxy, _ := request["proxy"].(string); proxy != converter.proxyURL {
		t.Fatalf("browser proxy = %q, want the configured %q", proxy, converter.proxyURL)
	}
}
