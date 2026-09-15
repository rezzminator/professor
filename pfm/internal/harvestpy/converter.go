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

// Runtime identifies the pinned Python executable and worker script. The
// script must be a provisioned, digest-verified file; it is never materialized
// in a shared temporary directory by the converter.
type Runtime struct {
	Python string
	Script string
	// PDFOCR / PDFLayout are harvester.config.json convert.pdfOcr /
	// convert.pdfLayout, handed to converter.py through its own
	// HARVESTER_PDF_* protocol variables (workerEnv).
	PDFOCR    bool
	PDFLayout bool
}

// converterProtocolEnv are the variables converter.py reads. The worker
// environment carries them ONLY from Runtime — a value inherited from the pfm
// process is stripped, so harvester.config.json stays the one source.
var converterProtocolEnv = []string{"HARVESTER_PDF_OCR", "HARVESTER_PDF_LAYOUT"}

// workerEnv is the converter process environment: the parent environment
// minus the converter protocol variables, plus the configured flags.
func workerEnv(parent []string, runtime Runtime) []string {
	env := make([]string, 0, len(parent)+2)
	for _, entry := range parent {
		name, _, _ := strings.Cut(entry, "=")
		protocol := false
		for _, reserved := range converterProtocolEnv {
			if name == reserved {
				protocol = true
				break
			}
		}
		if !protocol {
			env = append(env, entry)
		}
	}
	if runtime.PDFOCR {
		env = append(env, "HARVESTER_PDF_OCR=1")
	}
	if runtime.PDFLayout {
		env = append(env, "HARVESTER_PDF_LAYOUT=1")
	}
	return env
}

// Request is the one protocol request for every supported document kind. Go's
// archive boundary must pass a bounded extracted regular file and its actual
// document kind; archive kinds are rejected here.
type Request struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Source string `json:"source,omitempty"`
	OCR    bool   `json:"ocr,omitempty"`
	Layout bool   `json:"layout,omitempty"`
}

// Result is one successful conversion result and its optional feature prices.
type Result struct {
	Markdown string        `json:"markdown"`
	Kind     string        `json:"kind"`
	Features FeatureStatus `json:"features"`
}

// Converter runs exactly one pinned Python worker path.  There is no Go
// fallback converter: a worker or dependency failure is returned to the caller.
type Converter struct {
	runtime Runtime
	mu      sync.Mutex
	worker  *workerProcess
}

type workerProcess struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  lockedBuffer
}

// lockedBuffer is the stderr sink a worker subprocess fills from os/exec's
// own pipe-copy goroutine for the life of the process, while the error paths
// read it MID-FLIGHT to decorate their messages. A bare bytes.Buffer there is
// a data race between that copy goroutine's Write and stderrTail's String —
// the -race sweep catches it in both the conversion and the browser worker.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *lockedBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(p)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func NewConverter(runtime Runtime) *Converter {
	return &Converter{runtime: runtime}
}

func (converter *Converter) scriptPath() (string, error) {
	path := converter.runtime.Script
	if path == "" {
		return "", errors.New("harvestpy converter script path is empty; use the provisioned managed script")
	}
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() || !info.Mode().IsRegular() {
			return "", fmt.Errorf("harvestpy converter script is not a regular file: %s", path)
		}
		return path, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("harvestpy converter script is not provisioned: %s", path)
	} else {
		return "", fmt.Errorf("stat harvestpy converter script %s: %w", path, err)
	}
}

func (converter *Converter) Convert(ctx context.Context, request Request) (Result, error) {
	if request.Path == "" {
		return Result{}, errors.New("harvestpy conversion path is empty")
	}
	if isArchiveKind(request.Kind) {
		return Result{}, fmt.Errorf(
			"harvestpy rejects archive kind %q; Go must extract one bounded member first",
			request.Kind,
		)
	}
	return converter.run(ctx, request)
}

func isArchiveKind(kind string) bool {
	switch strings.ToLower(strings.TrimPrefix(kind, ".")) {
	case "zip", "tar", "gz", "tgz", "7z", "rar":
		return true
	default:
		return false
	}
}

func (converter *Converter) run(ctx context.Context, request Request) (Result, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return Result{}, fmt.Errorf("marshal harvestpy request: %w", err)
	}
	line, stderr, err := converter.request(ctx, body)
	if err != nil {
		return Result{}, err
	}
	var response struct {
		OK       bool          `json:"ok"`
		Markdown string        `json:"markdown"`
		Kind     string        `json:"kind"`
		Features FeatureStatus `json:"features"`
		Error    string        `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return Result{}, fmt.Errorf("decode harvestpy response JSON: %w (stderr: %s)", err, stderr)
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "worker returned ok=false without error"
		}
		return Result{}, errors.New(response.Error)
	}
	if response.Markdown == "" {
		return Result{}, errors.New("harvestpy worker returned empty markdown (EMPTY-text conversion)")
	}
	return Result{Markdown: response.Markdown, Kind: response.Kind, Features: response.Features}, nil
}

// request sends one JSON line through the long-lived worker. Requests are
// serialized so lazy imports and the Docling singleton persist exactly like
// the old MCP process. A crash or cancellation discards the process; the next
// request starts a clean worker with the original environment snapshot.
func (converter *Converter) request(ctx context.Context, body []byte) ([]byte, string, error) {
	converter.mu.Lock()
	defer converter.mu.Unlock()
	worker, err := converter.ensureWorkerLocked()
	if err != nil {
		return nil, "", err
	}
	if _, err := worker.stdin.Write(append(body, '\n')); err != nil {
		stderr := strings.TrimSpace(worker.stderr.String())
		converter.stopWorkerLocked()
		return nil, stderr, fmt.Errorf("harvestpy worker write failed: %w (stderr: %s)", err, stderr)
	}
	result := make(chan struct {
		line []byte
		err  error
	}, 1)
	go func() {
		line, err := worker.stdout.ReadBytes('\n')
		result <- struct {
			line []byte
			err  error
		}{line: bytes.TrimSpace(line), err: err}
	}()
	select {
	case <-ctx.Done():
		converter.stopWorkerLocked()
		return nil, "", fmt.Errorf("harvestpy worker request cancelled: %w", ctx.Err())
	case response := <-result:
		if response.err != nil {
			stderr := strings.TrimSpace(worker.stderr.String())
			converter.stopWorkerLocked()
			return nil, stderr, fmt.Errorf("harvestpy worker read failed: %w (stderr: %s)", response.err, stderr)
		}
		return response.line, strings.TrimSpace(worker.stderr.String()), nil
	}
}

func (converter *Converter) ensureWorkerLocked() (*workerProcess, error) {
	if converter.worker != nil {
		return converter.worker, nil
	}
	if strings.TrimSpace(converter.runtime.Python) == "" {
		return nil, errors.New("harvestpy interpreter path is empty; use the provisioned managed interpreter")
	}
	script, err := converter.scriptPath()
	if err != nil {
		return nil, err
	}
	command := exec.Command(converter.runtime.Python, script)
	command.Env = workerEnv(os.Environ(), converter.runtime)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open harvestpy worker stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open harvestpy worker stdout: %w", err)
	}
	worker := &workerProcess{command: command, stdin: stdin, stdout: bufio.NewReader(stdout)}
	command.Stderr = &worker.stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start harvestpy worker: %w", err)
	}
	converter.worker = worker
	return worker, nil
}

func (converter *Converter) stopWorkerLocked() {
	if converter.worker == nil {
		return
	}
	worker := converter.worker
	converter.worker = nil
	_ = worker.stdin.Close()
	if worker.command.Process != nil {
		_ = worker.command.Process.Kill()
	}
	_ = worker.command.Wait()
}

// Close terminates the managed worker and is safe to call repeatedly.
func (converter *Converter) Close() error {
	converter.mu.Lock()
	defer converter.mu.Unlock()
	converter.stopWorkerLocked()
	return nil
}

// Smoke invokes the same worker with a no-download import check.
func (converter *Converter) Smoke(ctx context.Context) (map[string]any, error) {
	line, stderr, err := converter.request(ctx, []byte("{\"op\":\"smoke\"}"))
	if err != nil {
		return nil, fmt.Errorf("harvestpy smoke subprocess: %w", err)
	}
	var response map[string]any
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fmt.Errorf("decode harvestpy smoke JSON: %w (stderr: %s)", err, stderr)
	}
	if ok, _ := response["ok"].(bool); !ok {
		return response, fmt.Errorf("harvestpy smoke failed: %v", response)
	}
	return response, nil
}
