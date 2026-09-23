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
	"path/filepath"
	"strings"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Runtime identifies the pinned Python executable and worker script. The
// script must be a provisioned, digest-verified file; it is never materialized
// in a shared temporary directory by the converter.
type Runtime struct {
	Python string
	Script string
	Runner deps.Runner
	// PDFOCR / PDFLayout are harvester.config.json convert.pdfOcr /
	// convert.pdfLayout, handed to converter.py through its own
	// HARVESTER_PDF_* protocol variables (workerEnv).
	PDFOCR    bool
	PDFLayout bool
	// ModelRoot is where `pfm install` staged the OCR models (the
	// harvest-python state root's models/); empty derives it from Python's
	// place under that root. ModelStaging lets the worker download into it —
	// only the install's staging run sets it; every read runs offline.
	ModelRoot    string
	ModelStaging bool
}

// modelRootFor derives the staged-model directory from an interpreter living
// under <root>/env/<platform>/<digest>/project/.venv/bin: <root>/models.
func modelRootFor(python string) string {
	for dir := filepath.Dir(python); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "env" {
			return filepath.Join(filepath.Dir(dir), "models")
		}
	}
	return ""
}

// converterProtocolEnv are the variables converter.py reads. The worker
// environment carries them ONLY from Runtime — a value inherited from the pfm
// process is stripped, so harvester.config.json stays the one source.
var converterProtocolEnv = []string{
	"HARVESTER_PDF_OCR", "HARVESTER_PDF_LAYOUT", "HARVESTPY_MODEL_ROOT", "HARVESTPY_MODEL_STAGING",
}

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
	modelRoot := runtime.ModelRoot
	if modelRoot == "" {
		modelRoot = modelRootFor(runtime.Python)
	}
	if modelRoot != "" {
		env = append(env, "HARVESTPY_MODEL_ROOT="+modelRoot)
	}
	if runtime.ModelStaging {
		env = append(env, "HARVESTPY_MODEL_STAGING=1")
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
	// OCRLang is the caller's script for a scan (harvest.OCRLangFrom); ""
	// lets the document's text layer, /Lang or metadata decide.
	OCRLang string `json:"ocr_lang,omitempty"`
	Layout  bool   `json:"layout,omitempty"`
	// FullDOM asks for an HTML page's WHOLE DOM converted, boilerplate
	// included, instead of its extracted main content — the recall gate's
	// fallback (harvest.FullDOMConverter).
	FullDOM bool `json:"full_dom,omitempty"`
}

// Result is one successful conversion result and its optional feature prices.
type Result struct {
	Markdown string        `json:"markdown"`
	Kind     string        `json:"kind"`
	Features FeatureStatus `json:"features"`
}

// ErrConverterFailed is a conversion the sidecar could NOT complete: a
// docling/pymupdf/markitdown exception, an OOM, missing model weights, a
// corrupt input. It is deliberately distinct from ErrConverterEmpty — a
// crashed pipeline and a blank document are two different answers, and a
// ladder that cannot tell them apart shows the wall as the document.
var ErrConverterFailed = errors.New("harvestpy conversion failed")

// ErrConverterEmpty is a conversion that RAN and produced no text. The
// message is the EMPTY-text contract the fetch ladder already reads.
var ErrConverterEmpty = errors.New("harvestpy worker returned empty markdown (EMPTY-text conversion)")

// Converter runs exactly one pinned Python worker path.  There is no Go
// fallback converter: a worker or dependency failure is returned to the caller.
type Converter struct {
	runtime Runtime
	mu      sync.Mutex
	worker  *workerProcess
}

type workerProcess struct {
	process    deps.Process
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	stdoutPipe io.ReadCloser
	stderr     *lockedBuffer
	// obs is the lifecycle recorder for this one sidecar: start, each
	// request, stop, kill, exit and every stderr line, under comp=harvestpy.
	obs *obs.Process
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

// stopWorkerProcess ends one sidecar: close its pipes, kill its process GROUP
// — a worker whose library shells out (docling's model tooling, patchright's
// Chrome) orphans those children when only the direct child is signalled —
// falling back to the direct kill when the group signal is refused, then wait.
// Both workers share it: two copies of a kill ladder is how one of them
// quietly stops killing descendants.
func stopWorkerProcess(worker *workerProcess, label string) error {
	worker.obs.Stop("close")
	var cleanupErr error
	if err := worker.stdin.Close(); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close %s stdin: %w", label, err))
	}
	if err := worker.stdoutPipe.Close(); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close %s stdout: %w", label, err))
	}
	killErr := worker.process.KillGroup()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	if killErr != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("kill %s process group: %w", label, killErr))
		if err := worker.process.Kill(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("kill %s process: %w", label, err))
			worker.obs.Killed(err)
		} else {
			worker.obs.Killed(nil)
		}
	} else {
		worker.obs.Killed(nil)
	}
	waitErr := worker.process.Wait()
	worker.obs.Exited(waitErr)
	if waitErr != nil && cleanupErr != nil {
		// A successful group kill normally makes Wait return the signal status;
		// only report it when a kill itself failed, where it is diagnostic.
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("wait for %s: %w", label, waitErr))
	}
	return cleanupErr
}

func NewConverter(runtime Runtime) *Converter {
	return &Converter{runtime: runtime}
}

func (converter *Converter) scriptPath() (string, error) {
	path := converter.runtime.Script
	if path == "" {
		return "", errors.New("harvestpy converter script path is empty; use the provisioned managed script")
	}
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() || !info.Mode().IsRegular() {
			return "", fmt.Errorf("harvestpy converter script is not a regular file: %s", path)
		}
		return path, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("harvestpy converter script is not provisioned: %s", path)
	}
	return "", fmt.Errorf("stat harvestpy converter script %s: %w", path, err)
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
		OK         bool          `json:"ok"`
		Markdown   string        `json:"markdown"`
		Kind       string        `json:"kind"`
		Features   FeatureStatus `json:"features"`
		Error      string        `json:"error"`
		ErrorClass string        `json:"error_class"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return Result{}, fmt.Errorf("decode harvestpy response JSON: %w (stderr: %s)", err, stderr)
	}
	if !response.OK {
		return Result{}, converterFailure(response.ErrorClass, response.Error, stderr)
	}
	if response.Markdown == "" {
		return Result{}, ErrConverterEmpty
	}
	return Result{Markdown: response.Markdown, Kind: response.Kind, Features: response.Features}, nil
}

// converterFailure names one ok:false answer as ErrConverterFailed carrying
// the sidecar's exception CLASS and the capped stderr tail — the same tail the
// write/read/decode branches of request() splice in, so a failure that only
// printed to stderr is still visible in the error a caller reads.
func converterFailure(class, message, stderr string) error {
	if class == "" {
		class = "unknown"
	}
	if strings.TrimSpace(message) == "" {
		message = "worker returned ok=false without error"
	}
	return fmt.Errorf("%w (%s): %s (stderr: %s)", ErrConverterFailed, class, message, stderrTail(stderr))
}

// request sends one JSON line through the long-lived worker. Requests are
// serialized so lazy imports and the Docling singleton persist exactly like
// the old MCP process. A crash or cancellation discards the process; the next
// request starts a clean worker with the original environment snapshot.
func (converter *Converter) request(ctx context.Context, body []byte) (line []byte, tail string, returnErr error) {
	converter.mu.Lock()
	defer converter.mu.Unlock()
	worker, err := converter.ensureWorkerLocked()
	if err != nil {
		return nil, "", err
	}
	end := worker.obs.Request("convert")
	defer func() { end(len(line), returnErr) }()
	payload := append(append([]byte(nil), body...), '\n')
	writeResult := make(chan error, 1)
	go func() {
		_, err := worker.stdin.Write(payload)
		writeResult <- err
	}()
	select {
	case err := <-writeResult:
		if err != nil {
			stderr := stderrTail(worker.stderr.String())
			cleanupErr := converter.stopWorkerLocked()
			return nil, stderr, fmt.Errorf(
				"harvestpy worker write failed: %w (stderr: %s; cleanup: %v)",
				err,
				stderr,
				cleanupErr,
			)
		}
	case <-ctx.Done():
		cleanupErr := converter.stopWorkerLocked()
		if cleanupErr != nil {
			return nil, "", fmt.Errorf("harvestpy worker request cancelled: %w (cleanup: %v)", ctx.Err(), cleanupErr)
		}
		return nil, "", fmt.Errorf("harvestpy worker request cancelled: %w", ctx.Err())
	}
	result := make(chan struct {
		line []byte
		err  error
	}, 1)
	go func() {
		line, err := readLineBounded(worker.stdout, converterResponseLimit)
		result <- struct {
			line []byte
			err  error
		}{line: bytes.TrimSpace(line), err: err}
	}()
	select {
	case <-ctx.Done():
		cleanupErr := converter.stopWorkerLocked()
		if cleanupErr != nil {
			return nil, "", fmt.Errorf("harvestpy worker request cancelled: %w (cleanup: %v)", ctx.Err(), cleanupErr)
		}
		return nil, "", fmt.Errorf("harvestpy worker request cancelled: %w", ctx.Err())
	case response := <-result:
		if response.err != nil {
			stderr := stderrTail(worker.stderr.String())
			cleanupErr := converter.stopWorkerLocked()
			return nil, stderr, fmt.Errorf(
				"harvestpy worker read failed: %w (stderr: %s; cleanup: %v)",
				response.err,
				stderr,
				cleanupErr,
			)
		}
		return response.line, stderrTail(worker.stderr.String()), nil
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
	runner := converter.runtime.Runner
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	processObs := obs.NewProcess(context.Background(), "converter")
	stderr := &lockedBuffer{}
	process, err := runner.Start(context.Background(), []string{converter.runtime.Python, script}, deps.StartOptions{
		Env:        workerEnv(os.Environ(), converter.runtime),
		StdinPipe:  true,
		StdoutPipe: true,
		// Its OWN process group, like the browser worker's: a conversion
		// dependency that shells out (docling's model tooling) leaves orphans
		// behind when only the direct child is signalled.
		ProcessGroup: true,
		Stderr:       processObs.Stderr(stderr),
	})
	if err != nil {
		processObs.Started(0, err)
		return nil, fmt.Errorf("start harvestpy worker: %w", err)
	}
	processObs.Started(process.Pid(), nil)
	stdin, err := process.StdinPipe()
	if err != nil {
		_ = process.KillGroup()
		_ = process.Wait()
		return nil, fmt.Errorf("open harvestpy worker stdin: %w", err)
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = process.KillGroup()
		_ = process.Wait()
		return nil, fmt.Errorf("open harvestpy worker stdout: %w", err)
	}
	worker := &workerProcess{
		process:    process,
		stdin:      stdin,
		stdout:     bufio.NewReader(stdout),
		stdoutPipe: stdout,
		stderr:     stderr,
		obs:        processObs,
	}
	converter.worker = worker
	return worker, nil
}

func (converter *Converter) stopWorkerLocked() error {
	if converter.worker == nil {
		return nil
	}
	worker := converter.worker
	converter.worker = nil
	return stopWorkerProcess(worker, "converter worker")
}

// Close terminates the managed worker and is safe to call repeatedly.
func (converter *Converter) Close() error {
	converter.mu.Lock()
	defer converter.mu.Unlock()
	return converter.stopWorkerLocked()
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
