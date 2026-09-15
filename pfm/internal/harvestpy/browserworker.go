package harvestpy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// AskReply is one answer from the SSRF authority for a worker guard ask.
type AskReply struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

// browserAsk is one guard ask emitted by the browser worker before its final
// response line.
type browserAsk struct {
	Ask string `json:"ask"`
	URL string `json:"url"`
}

// BrowserFetchRequest is one real-browser fetch request. Proxy may be empty
// for a direct connection; it is threaded through verbatim, never configured
// here — procuring an exit is a separate decision.
type BrowserFetchRequest struct {
	URL      string `json:"url"`
	Proxy    string `json:"proxy,omitempty"`
	Headless bool   `json:"headless"`
	// HostResolverRules pins Chrome's own DNS to the address the Go side
	// already resolved and validated (a "MAP host ip" rule). Chrome otherwise
	// resolves independently through the system resolver, which on a network
	// that rewrites DNS answers would send the browser rung to a block page
	// while every HTTP rung reached the real host. Empty leaves Chrome's
	// resolution alone.
	HostResolverRules string `json:"host_resolver_rules,omitempty"`
	TimeoutMS         int    `json:"timeout_ms,omitempty"`
}

// browserWorkerRequest is the wire shape of one worker op.
type browserWorkerRequest struct {
	Op string `json:"op"`
	BrowserFetchRequest
}

// BrowserWorker runs the opt-in Patchright + system-Chrome worker. It owns a
// SEPARATE process and mutex from Converter: a browser fetch can take 45 s and
// must never block document conversion behind the conversion worker's mutex.
type BrowserWorker struct {
	runtime Runtime
	mu      sync.Mutex
	worker  *workerProcess
}

func NewBrowserWorker(runtime Runtime) *BrowserWorker {
	return &BrowserWorker{runtime: runtime}
}

// Close terminates the managed browser worker and is safe to call repeatedly.
func (worker *BrowserWorker) Close() error {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	worker.stopWorkerLocked()
	return nil
}

// Fetch renders source in system Chrome through the interactive stdio
// protocol. Every URL Chrome touches arrives as an ask; onAsk is the SSRF
// authority (harvest.AssertFetchable at the adapter layer). A nil onAsk
// refuses every ask fail-closed.
func (worker *BrowserWorker) Fetch(
	ctx context.Context,
	source, proxy string,
	headless bool,
	timeoutMS int,
	onAsk func(url string) error,
) (string, int, error) {
	return worker.FetchPinned(ctx, source, proxy, "", headless, timeoutMS, onAsk)
}

// FetchPinned is Fetch with Chrome's resolver pinned to an already-validated
// address (see BrowserFetchRequest.HostResolverRules). An empty rule behaves
// exactly like Fetch.
func (worker *BrowserWorker) FetchPinned(
	ctx context.Context,
	source, proxy, hostResolverRules string,
	headless bool,
	timeoutMS int,
	onAsk func(url string) error,
) (string, int, error) {
	if strings.TrimSpace(source) == "" {
		return "", 0, errors.New("browser fetch url is empty")
	}
	body, err := json.Marshal(
		browserWorkerRequest{
			Op: "fetch",
			BrowserFetchRequest: BrowserFetchRequest{
				URL:               source,
				Proxy:             proxy,
				Headless:          headless,
				HostResolverRules: hostResolverRules,
				TimeoutMS:         timeoutMS,
			},
		},
	)
	if err != nil {
		return "", 0, fmt.Errorf("marshal browser fetch request: %w", err)
	}
	line, stderr, err := worker.requestInteractive(ctx, body, onAsk)
	if err != nil {
		return "", 0, err
	}
	var response struct {
		OK       bool   `json:"ok"`
		HTML     string `json:"html"`
		Status   int    `json:"status"`
		Headless bool   `json:"headless"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return "", 0, fmt.Errorf("decode browser worker response JSON: %w (stderr: %s)", err, stderr)
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "browser worker returned ok=false without error"
		}
		return "", 0, errors.New(response.Error)
	}
	return response.HTML, response.Status, nil
}

// Smoke invokes the browser worker's no-launch importability probe.
func (worker *BrowserWorker) Smoke(ctx context.Context) (map[string]any, error) {
	line, stderr, err := worker.requestInteractive(ctx, []byte(`{"op":"smoke"}`), func(string) error {
		return errors.New("smoke never asks")
	})
	if err != nil {
		return nil, fmt.Errorf("browser smoke subprocess: %w", err)
	}
	var response map[string]any
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fmt.Errorf("decode browser smoke JSON: %w (stderr: %s)", err, stderr)
	}
	if ok, _ := response["ok"].(bool); !ok {
		return response, fmt.Errorf("browser smoke failed: %v", response)
	}
	return response, nil
}

// requestInteractive sends one JSON line and then loops: a line carrying an
// "ask" is answered through onAsk (which consults harvest.AssertFetchable) and
// the loop continues; any other line is the final response. Requests are
// serialized under this worker's OWN mutex so a slow page cannot block the
// conversion worker.
// stderrTail caps worker stderr before it is interpolated into a
// user-visible error: browser.py logs refused targets, and a full transcript
// could carry internal hostnames or credentialed URLs into the tool response
// an agent reads.
func stderrTail(stderr string) string {
	const maxTailBytes = 500
	stderr = strings.TrimSpace(stderr)
	if len(stderr) <= maxTailBytes {
		return stderr
	}
	return "… " + stderr[len(stderr)-maxTailBytes:]
}

func (worker *BrowserWorker) requestInteractive(
	ctx context.Context,
	body []byte,
	onAsk func(url string) error,
) ([]byte, string, error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	browser, err := worker.ensureWorkerLocked()
	if err != nil {
		return nil, "", err
	}
	if _, err := browser.stdin.Write(append(body, '\n')); err != nil {
		stderr := stderrTail(browser.stderr.String())
		worker.stopWorkerLocked()
		return nil, stderr, fmt.Errorf("browser worker write failed: %w (stderr: %s)", err, stderr)
	}
	type readResult struct {
		line []byte
		err  error
	}
	read := make(chan readResult, 1)
	for {
		go func() {
			line, err := browser.stdout.ReadBytes('\n')
			read <- readResult{line: bytes.TrimSpace(line), err: err}
		}()
		select {
		case <-ctx.Done():
			worker.stopWorkerLocked()
			return nil, "", fmt.Errorf("browser worker request cancelled: %w", ctx.Err())
		case result := <-read:
			if result.err != nil {
				stderr := stderrTail(browser.stderr.String())
				worker.stopWorkerLocked()
				return nil, stderr, fmt.Errorf("browser worker read failed: %w (stderr: %s)", result.err, stderr)
			}
			var ask browserAsk
			if unmarshalErr := json.Unmarshal(result.line, &ask); unmarshalErr == nil && ask.Ask == "fetchable" {
				reply := AskReply{Allow: false, Reason: "no SSRF handler was configured"}
				if onAsk != nil {
					if askErr := onAsk(ask.URL); askErr == nil {
						reply = AskReply{Allow: true}
					} else {
						reply = AskReply{Allow: false, Reason: askErr.Error()}
					}
				}
				answer, marshalErr := json.Marshal(reply)
				if marshalErr != nil {
					return nil, "", fmt.Errorf("marshal browser ask reply: %w", marshalErr)
				}
				if _, writeErr := browser.stdin.Write(append(answer, '\n')); writeErr != nil {
					stderr := stderrTail(browser.stderr.String())
					worker.stopWorkerLocked()
					return nil, stderr, fmt.Errorf("browser worker ask reply failed: %w (stderr: %s)", writeErr, stderr)
				}
				continue
			}
			return result.line, stderrTail(browser.stderr.String()), nil
		}
	}
}

// browserWorkerCommand is injectable for protocol tests; production validates
// the provisioned script is a regular file, then execs the pinned interpreter.
var browserWorkerCommand = func(python, script string) (*exec.Cmd, error) {
	info, statErr := os.Stat(script)
	if statErr != nil || info.IsDir() || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("browser worker script is not provisioned: %s", script)
	}
	return exec.Command(python, script), nil
}

func (worker *BrowserWorker) ensureWorkerLocked() (*workerProcess, error) {
	if worker.worker != nil {
		return worker.worker, nil
	}
	if strings.TrimSpace(worker.runtime.Python) == "" {
		return nil, errors.New(
			"browser interpreter path is empty; the browser environment is NOT provisioned (it provisions on the first browser fetch once fetch.browser is true in harvester.config.json)",
		)
	}
	command, err := browserWorkerCommand(worker.runtime.Python, worker.runtime.Script)
	if err != nil {
		return nil, err
	}
	configureBrowserCommand(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open browser worker stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open browser worker stdout: %w", err)
	}
	process := &workerProcess{command: command, stdin: stdin, stdout: bufio.NewReader(stdout)}
	command.Stderr = &process.stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start browser worker: %w", err)
	}
	worker.worker = process
	return process, nil
}

func (worker *BrowserWorker) stopWorkerLocked() {
	if worker.worker == nil {
		return
	}
	process := worker.worker
	worker.worker = nil
	_ = process.stdin.Close()
	if process.command.Process != nil {
		// Kill the whole process GROUP: patchright's node driver and the Chrome
		// it launched are children of the python worker, and killing only the
		// direct child leaves Chrome reparented and alive.
		if err := killBrowserProcessGroup(process.command.Process.Pid); err != nil {
			_ = process.command.Process.Kill()
		}
	}
	_ = process.command.Wait()
}

var _ io.Closer = (*BrowserWorker)(nil)
