package harvestpy

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/deps"
)

// realPythonConverter runs the EMBEDDED converter.py under the host's own
// python3. Every case below reaches a branch of convert() that touches no
// third-party library, so it needs no provisioned environment; a host without
// python3 is a NAMED skip, never a quiet pass.
func realPythonConverter(t *testing.T) *Converter {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("named gap: python3 is unavailable on this host; the converter failure contract did not run")
	}
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: python, Script: script})
	t.Cleanup(func() { _ = converter.Close() })
	return converter
}

// TestConverterExceptionIsAFailureNotAnEmptyConversion is L2-F3: converter.py
// caught every conversion exception, blanked the markdown and answered
// ok:true, so the Go side reported a crashed docling/pymupdf/markitdown
// pipeline as "this document converted to nothing" — an error rendered as
// absence, with the stderr tail dropped on the floor.
func TestConverterExceptionIsAFailureNotAnEmptyConversion(t *testing.T) {
	converter := realPythonConverter(t)
	document := filepath.Join(t.TempDir(), "paper.zzz")
	if err := os.WriteFile(document, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := converter.Convert(context.Background(), Request{Path: document, Kind: "zzz"})
	if err == nil {
		t.Fatal("a raising conversion returned no error")
	}
	if !errors.Is(err, ErrConverterFailed) {
		t.Fatalf("Convert() error = %v, want ErrConverterFailed", err)
	}
	if errors.Is(err, ErrConverterEmpty) || strings.Contains(err.Error(), "EMPTY-text") {
		t.Fatalf("a crashed conversion still reads as an EMPTY document: %v", err)
	}
	if !strings.Contains(err.Error(), "ValueError") {
		t.Fatalf("the exception class is missing from the error: %v", err)
	}
	if !strings.Contains(err.Error(), "stderr:") {
		t.Fatalf("the stderr tail sibling branches splice in is missing: %v", err)
	}
}

// TestConverterFailureCarriesTheBasenameNotTheFullPath: the library exception
// text carries the document's absolute path (FileNotFoundError does), and the
// message reaches a tool answer an agent reads. Only the basename may survive.
func TestConverterFailureCarriesTheBasenameNotTheFullPath(t *testing.T) {
	converter := realPythonConverter(t)
	directory := t.TempDir()
	missing := filepath.Join(directory, "confidential-report.json")
	_, err := converter.Convert(context.Background(), Request{Path: missing, Kind: "json"})
	if err == nil {
		t.Fatal("converting a missing document returned no error")
	}
	if !errors.Is(err, ErrConverterFailed) {
		t.Fatalf("Convert() error = %v, want ErrConverterFailed", err)
	}
	if !strings.Contains(err.Error(), "FileNotFoundError") {
		t.Fatalf("the exception class is missing from the error: %v", err)
	}
	if strings.Contains(err.Error(), directory) {
		t.Fatalf("the document's full path leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "confidential-report.json") {
		t.Fatalf("the basename was stripped too — nothing names the document: %v", err)
	}
}

// TestConverterEmptyDocumentStaysADistinctOutcome: the other half of L2-F3 —
// a document that genuinely converts to nothing must NOT be reported as a
// crash. The worker here is a fake speaking the pipe protocol, so the outcome
// is the Go mapping alone.
func TestConverterEmptyDocumentStaysADistinctOutcome(t *testing.T) {
	converter := fakeLineConverter(t, `{"ok":true,"markdown":"","kind":"html"}`)
	_, err := converter.Convert(context.Background(), Request{Path: "doc.html", Kind: "html"})
	if !errors.Is(err, ErrConverterEmpty) {
		t.Fatalf("Convert() error = %v, want ErrConverterEmpty", err)
	}
	if errors.Is(err, ErrConverterFailed) {
		t.Fatalf("an empty document was reported as a converter failure: %v", err)
	}
}

// TestConverterOKFalseWithoutAClassStillNamesTheFailure: an older provisioned
// worker answers ok:false with no error_class. The outcome is still a named
// converter failure, never a nil error and never a silent empty result.
func TestConverterOKFalseWithoutAClassStillNamesTheFailure(t *testing.T) {
	converter := fakeLineConverter(t, `{"ok":false}`)
	_, err := converter.Convert(context.Background(), Request{Path: "doc.html", Kind: "html"})
	if !errors.Is(err, ErrConverterFailed) {
		t.Fatalf("Convert() error = %v, want ErrConverterFailed", err)
	}
	if !strings.Contains(err.Error(), "worker returned ok=false without error") {
		t.Fatalf("an ok:false with no message says nothing about why: %v", err)
	}
}

// TestConverterWorkerRunsInItsOwnProcessGroup is L2-F31: the conversion worker
// started WITHOUT ProcessGroup and was stopped with Kill(), so a converter
// dependency that shells out orphaned its children — the browser worker has
// had both since it shipped.
func TestConverterWorkerRunsInItsOwnProcessGroup(t *testing.T) {
	runner := lineRunner(t, `{"ok":true,"markdown":"# converted"}`)
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: "fake-python", Script: script, Runner: runner})
	if _, err := converter.Convert(context.Background(), Request{Path: "doc.html", Kind: "html"}); err != nil {
		t.Fatal(err)
	}
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("Start calls = %d, want 1: %+v", len(starts), starts)
	}
	if !starts[0].Opts.ProcessGroup {
		t.Fatal("the conversion worker started outside its own process group — a shelling dependency orphans children")
	}
	if err := converter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	calls := runner.LifecycleCalls()
	if len(calls) != 2 || calls[0].Action != "kill-group" || calls[1].Action != "wait" {
		t.Fatalf("lifecycle calls = %+v, want kill-group, wait", calls)
	}
}

// TestConverterFallsBackToDirectKillWhenGroupKillFails mirrors the browser
// worker's ladder on the now-shared stop path: a refused group signal is
// reported AND followed by the direct kill, never swallowed.
func TestConverterFallsBackToDirectKillWhenGroupKillFails(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdinReader.Close()
		_ = stdoutWriter.Close()
	})
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-python"}, deps.InteractiveScript{
		Pid:      7102,
		Stdin:    stdinWriter,
		Stdout:   stdoutReader,
		GroupErr: errors.New("group kill denied"),
	})
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: "fake-python", Script: script, Runner: runner})
	if _, err := converter.ensureWorkerLocked(); err != nil {
		t.Fatalf("ensureWorkerLocked() error = %v", err)
	}
	err := converter.Close()
	if err == nil || !strings.Contains(err.Error(), "kill converter worker process group") {
		t.Fatalf("Close() error = %v, want a contextual group-kill error", err)
	}
	calls := runner.LifecycleCalls()
	if len(calls) != 3 || calls[0].Action != "kill-group" || calls[1].Action != "kill" || calls[2].Action != "wait" {
		t.Fatalf("lifecycle calls = %+v, want kill-group, kill, wait", calls)
	}
}

// TestConverterRequestCapsStderrTheSameWayTheBrowserWorkerDoes is the
// converter sibling of browserworker.go's requestInteractive: on a write
// failure, requestInteractive tails worker stderr through stderrTail before
// splicing it into the returned error, but converter.request only ran
// strings.TrimSpace — an unbounded sidecar stderr (a stack trace, a path, a
// credentialed URL docling logged) reached the tool answer whole instead of
// capped at stderrTail's bound.
func TestConverterRequestCapsStderrTheSameWayTheBrowserWorkerDoes(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdoutWriter.Close()
	})
	// Closing the READ half before Start makes every future write to
	// stdinWriter fail immediately (io.ErrClosedPipe) — a write failure
	// without needing a real process on the other end.
	if err := stdinReader.Close(); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-python"}, deps.InteractiveScript{
		Pid: 7201, Stdin: stdinWriter, Stdout: stdoutReader,
	})
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: "fake-python", Script: script, Runner: runner})
	worker, err := converter.ensureWorkerLocked()
	if err != nil {
		t.Fatalf("ensureWorkerLocked() error = %v", err)
	}
	// Simulate an oversized sidecar stderr the way a real docling stack trace
	// would accumulate one, byte by byte, in the buffer obs.Process.Stderr
	// wires the child's stderr pipe to. 2000 bytes is well past stderrTail's
	// 500-byte cap.
	const overLongBytes = 2000
	overLong := strings.Repeat("X", overLongBytes)
	if _, err := worker.stderr.Write([]byte(overLong)); err != nil {
		t.Fatal(err)
	}
	_, tail, err := converter.request(context.Background(), []byte(`{"op":"convert"}`))
	if err == nil {
		t.Fatal("a write on a closed stdin pipe returned no error")
	}
	if len(tail) >= overLongBytes {
		t.Fatalf("returned stderr tail is %d bytes, want capped well below the %d-byte input: %q",
			len(tail), overLongBytes, tail)
	}
	if strings.Contains(err.Error(), overLong) {
		t.Fatalf("the full uncapped stderr reached the error: %v", err)
	}
	if !strings.Contains(err.Error(), "…") {
		t.Fatalf("the error's stderr was not marked as truncated: %v", err)
	}
}

// fakeLineConverter answers every request with one fixed protocol line, from a
// scripted deps.Runner — no Python, no real process.
func fakeLineConverter(t *testing.T, response string) *Converter {
	t.Helper()
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{
		Python: "fake-python",
		Script: script,
		Runner: lineRunner(t, response),
	})
	t.Cleanup(func() { _ = converter.Close() })
	return converter
}
