package obs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// compHarvestpy is the component every sidecar lifecycle record belongs to.
const compHarvestpy = "harvestpy"

// Process is the harvestpy middleware (spec § Middleware): the lifecycle of
// one Python sidecar — the conversion worker, the browser worker, an
// interpreter check or a provisioning command — as records under
// comp=harvestpy: start, each request, stop, kill, exit, and every stderr line
// as a WARN. It records the operation and its shape (kind, pid, subcmd, bytes,
// exit, dur_ms, err); a request body, a response line or a document never
// reaches it.
type Process struct {
	ctx     context.Context
	logger  *slog.Logger
	timing  clock.Clock
	kind    string
	pid     int
	started time.Time
	stderr  *lineWriter
}

// NewProcess prepares the recorder for one sidecar of the given kind
// (converter, browser, check, provision). Nothing is written until Started.
func NewProcess(ctx context.Context, kind string) *Process {
	scoped := Component(ctx, compHarvestpy)
	return &Process{ctx: scoped, logger: Logger(scoped), timing: current(ctx).timing, kind: kind}
}

// Started records the start: op=start with the pid at INFO, or the start
// error at ERROR when the process never came up.
func (process *Process) Started(pid int, err error) {
	process.pid = pid
	process.started = process.timing.Now()
	process.log("start", err)
}

// Request opens one request to the sidecar under subcmd and returns the
// function that records its answer: the bytes read back, dur_ms, and err (at
// ERROR when set). Call it exactly once per request.
func (process *Process) Request(subcmd string) func(size int, err error) {
	started := process.timing.Now()
	return func(size int, err error) {
		process.log("request", err,
			slog.String("subcmd", subcmd),
			slog.Int("bytes", size),
			slog.Int64(FieldDur, process.timing.Now().Sub(started).Milliseconds()),
		)
	}
}

// Stderr returns the writer to hand the sidecar as its stderr: every byte
// passes through to next unchanged, and every complete line is one WARN
// record (op=stderr, class=<leading word>, bytes=<line length>) — the only
// free-text content this middleware would otherwise carry, converter.py's
// exception text among it, which can hold a path or URL. At the default
// level the line itself never reaches the record, only its bounded class
// token and size; the full line=... is added only when comp=harvestpy is
// actually logging at DEBUG (Enabled), so it is opt-in per pfm.config.json's
// log.components, never on by default. A trailing partial line is flushed by
// Exited.
func (process *Process) Stderr(next io.Writer) io.Writer {
	process.stderr = &lineWriter{next: next, emit: func(line string) {
		attrs := []slog.Attr{
			slog.String("op", "stderr"), slog.String("kind", process.kind), slog.Int(FieldPID, process.pid),
			slog.Int("bytes", len(line)), slog.String("class", stderrLineClass(line)),
		}
		if Enabled(process.ctx, compHarvestpy, slog.LevelDebug) {
			attrs = append(attrs, slog.String("line", line))
		}
		process.logger.LogAttrs(process.ctx, slog.LevelWarn, "harvestpy.stderr", attrs...)
	}}
	return process.stderr
}

// stderrClassCap bounds the class token: a leading word with no whitespace
// could otherwise smuggle an entire unbroken line through the "shapes and
// sizes only" field.
const stderrClassCap = 40

// stderrLineClass is the bounded class token a harvestpy.stderr record
// carries at the default level in place of the free-text line: its leading
// whitespace-delimited word (Traceback, ERROR, ValueError:, …) with any
// trailing colon trimmed and capped at stderrClassCap — a shape, never the
// line's content. An all-whitespace line classes as "empty".
func stderrLineClass(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "empty"
	}
	word, _, _ := strings.Cut(trimmed, " ")
	word = strings.TrimRight(word, ":")
	if len(word) > stderrClassCap {
		word = word[:stderrClassCap]
	}
	return word
}

// Stop records the decision to end the sidecar and why (close, cancelled, a
// failed write) — before the kill, so a hung kill still leaves the intent.
func (process *Process) Stop(cause string) {
	process.log("stop", nil, slog.String("cause", cause))
}

// Killed records the kill: INFO when the signal went out, ERROR with the
// reason when it did not.
func (process *Process) Killed(err error) {
	process.log("kill", err)
}

// Exited records the terminal state from Wait's error: exit=0 at INFO for a
// clean exit, the exit code at WARN for a non-zero status, and any other
// error (a signal, a wait failure) at ERROR — with dur_ms since Started. Any
// partial stderr line still buffered is flushed first.
func (process *Process) Exited(err error) {
	if process.stderr != nil {
		process.stderr.flush()
	}
	elapsed := slog.Int64(FieldDur, process.timing.Now().Sub(process.started).Milliseconds())
	// The duck type, not the concrete *exec.ExitError: a sidecar started
	// through the deps.Runner seam never surfaces a bare *exec.ExitError
	// (deps.RealRunner.Run folds a completed-but-nonzero exit into
	// RunResult.ExitCode, then reports it back as the same "any error
	// naming its own exit code" shape deps.ExitCode reads), so a caller
	// that only matched *exec.ExitError silently fell through to the
	// generic ERROR branch for a command that actually answered.
	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) {
		process.logger.LogAttrs(process.ctx, slog.LevelWarn, "harvestpy.exit",
			slog.String("op", "exit"), slog.String("kind", process.kind), slog.Int(FieldPID, process.pid),
			slog.Int(FieldExit, exitErr.ExitCode()), elapsed,
		)
		return
	}
	attrs := []slog.Attr{elapsed}
	if err == nil {
		attrs = append(attrs, slog.Int(FieldExit, 0))
	}
	process.log("exit", err, attrs...)
}

// log writes one lifecycle record: op, kind, the pid once known, err at ERROR.
func (process *Process) log(op string, err error, attrs ...slog.Attr) {
	record := []slog.Attr{slog.String("op", op), slog.String("kind", process.kind)}
	if process.pid > 0 {
		record = append(record, slog.Int(FieldPID, process.pid))
	}
	record = append(record, attrs...)
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
		record = append(record, slog.String(FieldErr, err.Error()))
	}
	process.logger.LogAttrs(process.ctx, level, "harvestpy."+op, record...)
}

// lineWriter tees every byte to next and hands each complete line (CR and LF
// stripped) to emit. exec copies a child's stderr from its own goroutine, so
// the buffer is locked.
type lineWriter struct {
	mutex   sync.Mutex
	next    io.Writer
	emit    func(line string)
	partial bytes.Buffer
}

func (writer *lineWriter) Write(chunk []byte) (int, error) {
	written, err := writer.next.Write(chunk)
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	writer.partial.Write(chunk[:written])
	for {
		line, rest, found := bytes.Cut(writer.partial.Bytes(), []byte{'\n'})
		if !found {
			break
		}
		writer.emit(string(bytes.TrimRight(line, "\r")))
		remaining := append([]byte(nil), rest...)
		writer.partial.Reset()
		writer.partial.Write(remaining)
	}
	return written, err
}

// flush emits a trailing line the sidecar never terminated.
func (writer *lineWriter) flush() {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.partial.Len() > 0 {
		writer.emit(string(bytes.TrimRight(writer.partial.Bytes(), "\r")))
		writer.partial.Reset()
	}
}
