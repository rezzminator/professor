package harvestpy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/deps"
)

// lineRunner scripts a stdio worker that answers every request line with one
// fixed response line — the pipe protocol both sidecars speak, with no Python
// and no real process.
func lineRunner(t *testing.T, response string) *deps.FakeRunner {
	t.Helper()
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-python"}, deps.InteractiveScript{
		Pid:    7101,
		Stdin:  requestWriter,
		Stdout: responseReader,
	})
	go func() {
		reader := bufio.NewReader(requestReader)
		for {
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
			if _, err := fmt.Fprintln(responseWriter, response); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = requestReader.Close()
		_ = requestWriter.Close()
		_ = responseReader.Close()
		_ = responseWriter.Close()
	})
	return runner
}

// shrinkResponseLimits makes both sidecar budgets small enough to overrun in a
// test without moving megabytes through a pipe.
func shrinkResponseLimits(t *testing.T, limit int) {
	t.Helper()
	converterWas, browserWas := converterResponseLimit, browserResponseLimit
	converterResponseLimit, browserResponseLimit = limit, limit
	t.Cleanup(func() { converterResponseLimit, browserResponseLimit = converterWas, browserWas })
}

func TestReadLineBoundedRefusesALineOverItsLimit(t *testing.T) {
	reader := bufio.NewReader(bytes.NewReader(append(bytes.Repeat([]byte("x"), 4096), '\n')))
	if _, err := readLineBounded(reader, 1024); !errors.Is(err, ErrSidecarResponseTooLarge) {
		t.Fatalf("readLineBounded() error = %v, want ErrSidecarResponseTooLarge", err)
	}
}

func TestReadLineBoundedReadsALongLineUnderItsLimit(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 40_000)
	reader := bufio.NewReader(bytes.NewReader(append(append([]byte(nil), body...), '\n')))
	line, err := readLineBounded(reader, 1<<20)
	if err != nil {
		t.Fatalf("readLineBounded() error = %v", err)
	}
	// Far past bufio's 4096-byte buffer: a naive single ReadSlice would have
	// stopped at ErrBufferFull and reported a truncated protocol line.
	if len(line) != len(body)+1 {
		t.Fatalf("readLineBounded() read %d bytes, want %d", len(line), len(body)+1)
	}
}

func TestReadLineBoundedKeepsWhatAnEndedPipeSaid(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("half a line"))
	line, err := readLineBounded(reader, 1<<20)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("readLineBounded() error = %v, want io.EOF", err)
	}
	if string(line) != "half a line" {
		t.Fatalf("readLineBounded() dropped what the worker managed to say: %q", line)
	}
}

// TestConverterRefusesAnUnboundedResponseLine is L2-F30: converter.go read the
// sidecar's answer with bufio.ReadBytes('\n') and no ceiling at all.
func TestConverterRefusesAnUnboundedResponseLine(t *testing.T) {
	shrinkResponseLimits(t, 512)
	converter := fakeLineConverter(t, `{"ok":true,"markdown":"`+strings.Repeat("m", 4096)+`"}`)
	_, err := converter.Convert(context.Background(), Request{Path: "doc.html", Kind: "html"})
	if !errors.Is(err, ErrSidecarResponseTooLarge) {
		t.Fatalf("Convert() error = %v, want ErrSidecarResponseTooLarge", err)
	}
}

// TestBrowserWorkerRefusesAnUnboundedResponseLine is L2-F30's browser half:
// browserworker.go read every response AND every guard ask unbounded.
func TestBrowserWorkerRefusesAnUnboundedResponseLine(t *testing.T) {
	shrinkResponseLimits(t, 512)
	script := filepath.Join(t.TempDir(), "browser.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := NewBrowserWorker(Runtime{
		Python: "fake-python",
		Script: script,
		Runner: lineRunner(t, `{"ok":true,"status":200,"html":"`+strings.Repeat("h", 4096)+`"}`),
	})
	t.Cleanup(func() { _ = worker.Close() })
	_, _, err := worker.Fetch(
		context.Background(),
		"https://example.test/",
		"http://127.0.0.1:1/",
		true,
		45000,
		func(string) error { return nil },
	)
	if !errors.Is(err, ErrSidecarResponseTooLarge) {
		t.Fatalf("Fetch() error = %v, want ErrSidecarResponseTooLarge", err)
	}
}
