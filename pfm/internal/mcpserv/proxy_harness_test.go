package mcpserv

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

type proxyTestHarness struct {
	input  *io.PipeWriter
	output *bufio.Reader
	cancel context.CancelFunc
	done   chan error
}

func startProxyTestHarness(t *testing.T, proxy *stdioProxy) *proxyTestHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	input, inputWriter := io.Pipe()
	output, outputWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- proxy.run(ctx, input, outputWriter)
		_ = outputWriter.Close()
	}()
	harness := &proxyTestHarness{
		input: inputWriter, output: bufio.NewReader(output), cancel: cancel, done: done,
	}
	t.Cleanup(func() {
		_ = inputWriter.Close()
		go func() { _, _ = io.Copy(io.Discard, harness.output) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("stdio proxy did not stop after cancellation")
		}
		cancel()
	})
	return harness
}

func (harness *proxyTestHarness) write(t *testing.T, frame string) {
	t.Helper()
	if _, err := io.WriteString(harness.input, frame+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (harness *proxyTestHarness) read(t *testing.T) map[string]any {
	t.Helper()
	result := make(chan []byte, 1)
	errors := make(chan error, 1)
	go func() {
		line, err := harness.output.ReadBytes('\n')
		if err != nil {
			errors <- err
			return
		}
		result <- line
	}()
	select {
	case line := <-result:
		var frame map[string]any
		if err := json.Unmarshal(line, &frame); err != nil {
			t.Fatalf("decode proxy response %s: %v", line, err)
		}
		return frame
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for proxy response")
	}
	return nil
}

func TestProxyHarnessClosesDaemonSessionBeforeCleanupReturns(t *testing.T) {
	t.Parallel()
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			t.Errorf("method=%s, want DELETE", request.Method)
		}
		time.Sleep(30 * time.Millisecond)
		writer.WriteHeader(http.StatusNoContent)
		close(closed)
	}))
	defer server.Close()

	t.Run("proxy", func(t *testing.T) {
		proxy := &stdioProxy{endpoint: server.URL, client: obs.WrapClient(&http.Client{}), sessionID: "session"}
		startProxyTestHarness(t, proxy)
	})
	select {
	case <-closed:
	default:
		t.Fatal("daemon session DELETE had not completed when harness cleanup returned")
	}
}
