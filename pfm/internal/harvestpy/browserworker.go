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
	"strings"
	"sync"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/obs"
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
	return worker.stopWorkerLocked()
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
	line, stderr, err := worker.requestInteractive(ctx, "fetch", body, onAsk)
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
	line, stderr, err := worker.requestInteractive(ctx, "smoke", []byte(`{"op":"smoke"}`), func(string) error {
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
	op string,
	body []byte,
	onAsk func(url string) error,
) (line []byte, tail string, returnErr error) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	browser, err := worker.ensureWorkerLocked()
	if err != nil {
		return nil, "", err
	}
	end := browser.obs.Request(op)
	defer func() { end(len(line), returnErr) }()
	payload := append(append([]byte(nil), body...), '\n')
	writeResult := make(chan error, 1)
	go func() {
		_, err := browser.stdin.Write(payload)
		writeResult <- err
	}()
	select {
	case err := <-writeResult:
		if err != nil {
			stderr := stderrTail(browser.stderr.String())
			cleanupErr := worker.stopWorkerLocked()
			return nil, stderr, fmt.Errorf(
				"browser worker write failed: %w (stderr: %s; cleanup: %v)",
				err,
				stderr,
				cleanupErr,
			)
		}
	case <-ctx.Done():
		cleanupErr := worker.stopWorkerLocked()
		if cleanupErr != nil {
			return nil, "", fmt.Errorf("browser worker request cancelled: %w (cleanup: %v)", ctx.Err(), cleanupErr)
		}
		return nil, "", fmt.Errorf("browser worker request cancelled: %w", ctx.Err())
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
			cleanupErr := worker.stopWorkerLocked()
			if cleanupErr != nil {
				return nil, "", fmt.Errorf("browser worker request cancelled: %w (cleanup: %v)", ctx.Err(), cleanupErr)
			}
			return nil, "", fmt.Errorf("browser worker request cancelled: %w", ctx.Err())
		case result := <-read:
			if result.err != nil {
				stderr := stderrTail(browser.stderr.String())
				cleanupErr := worker.stopWorkerLocked()
				return nil, stderr, fmt.Errorf(
					"browser worker read failed: %w (stderr: %s; cleanup: %v)",
					result.err,
					stderr,
					cleanupErr,
				)
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
				writeResult := make(chan error, 1)
				payload := append(append([]byte(nil), answer...), '\n')
				go func() {
					_, writeErr := browser.stdin.Write(payload)
					writeResult <- writeErr
				}()
				select {
				case writeErr := <-writeResult:
					if writeErr == nil {
						continue
					}
					stderr := stderrTail(browser.stderr.String())
					cleanupErr := worker.stopWorkerLocked()
					return nil, stderr, fmt.Errorf(
						"browser worker ask reply failed: %w (stderr: %s; cleanup: %v)",
						writeErr,
						stderr,
						cleanupErr,
					)
				case <-ctx.Done():
					cleanupErr := worker.stopWorkerLocked()
					if cleanupErr != nil {
						return nil, "", fmt.Errorf(
							"browser worker ask reply cancelled: %w (cleanup: %v)",
							ctx.Err(),
							cleanupErr,
						)
					}
					return nil, "", fmt.Errorf("browser worker ask reply cancelled: %w", ctx.Err())
				}
			}
			return result.line, stderrTail(browser.stderr.String()), nil
		}
	}
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
	if worker.runtime.Runner == nil {
		info, err := os.Stat(worker.runtime.Script)
		if err != nil || info.IsDir() || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("browser worker script is not provisioned: %s", worker.runtime.Script)
		}
	}
	runner := worker.runtime.Runner
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	processObs := obs.NewProcess(context.Background(), "browser")
	stderr := &lockedBuffer{}
	process, err := runner.Start(
		context.Background(),
		[]string{worker.runtime.Python, worker.runtime.Script},
		deps.StartOptions{
			StdinPipe:    true,
			StdoutPipe:   true,
			ProcessGroup: true,
			Stderr:       processObs.Stderr(stderr),
		},
	)
	if err != nil {
		processObs.Started(0, err)
		return nil, fmt.Errorf("start browser worker: %w", err)
	}
	processObs.Started(process.Pid(), nil)
	stdin, err := process.StdinPipe()
	if err != nil {
		_ = process.KillGroup()
		_ = process.Wait()
		return nil, fmt.Errorf("open browser worker stdin: %w", err)
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = process.KillGroup()
		_ = process.Wait()
		return nil, fmt.Errorf("open browser worker stdout: %w", err)
	}
	processState := &workerProcess{
		process:    process,
		stdin:      stdin,
		stdout:     bufio.NewReader(stdout),
		stdoutPipe: stdout,
		stderr:     stderr,
		obs:        processObs,
	}
	worker.worker = processState
	return processState, nil
}

func (worker *BrowserWorker) stopWorkerLocked() error {
	if worker.worker == nil {
		return nil
	}
	process := worker.worker
	worker.worker = nil
	process.obs.Stop("close")
	var cleanupErr error
	if err := process.stdin.Close(); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close browser worker stdin: %w", err))
	}
	if err := process.stdoutPipe.Close(); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close browser worker stdout: %w", err))
	}
	killErr := process.process.KillGroup()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	if killErr != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("kill browser worker process group: %w", killErr))
		if err := process.process.Kill(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("kill browser worker process: %w", err))
			process.obs.Killed(err)
		} else {
			process.obs.Killed(nil)
		}
	} else {
		process.obs.Killed(nil)
	}
	waitErr := process.process.Wait()
	process.obs.Exited(waitErr)
	if waitErr != nil {
		// A successful group kill normally makes Wait return the signal status;
		// only report it when the kill itself failed, where it is diagnostic.
		if cleanupErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("wait for browser worker: %w", waitErr))
		}
	}
	return cleanupErr
}

var _ io.Closer = (*BrowserWorker)(nil)
