package harvestpy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/obs"
)

// TestProvisionCommandExitIsNeverRecordedAsZero is the harvestpy Exited() gap:
// deps.Runner reports an ordinary non-zero exit in RunResult.ExitCode with a
// NIL error, and both provisioning recorders handed that nil straight to
// obs.Process.Exited — so a `uv pip check` that exited 1 was recorded as
// exit=0 at INFO, a failure written down as a success.
//
// The record's own shape is WARN with the exit code and no err field — the
// duck-typed "any error naming its own exit code" contract
// internal/obs/process.go's Exited reads commandExitStatus through
// (interface{ ExitCode() int }), the same shape a bare *exec.ExitError takes:
// a completed command's own non-zero exit is the command ANSWERING, not an
// error of the wrapper. The failure text itself still reaches the CALLER —
// runCommandWithRunner's own returned error, asserted below — never the
// activity log.
func TestProvisionCommandExitIsNeverRecordedAsZero(t *testing.T) {
	ctx, recorder := obs.Test(t)
	runner := &deps.FakeRunner{}
	runner.Script(
		[]string{"uv", "pip", "check"},
		deps.RunResult{Stderr: []byte("torch is incompatible\n"), ExitCode: 1},
		nil,
	)
	_, err := runCommandWithRunner(ctx, runner, "uv", []string{"pip", "check"}, t.TempDir())
	if err == nil {
		t.Fatal("a non-zero uv exit returned no error")
	}
	if !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("the returned error says nothing about the exit code: %v", err)
	}

	records := harvestpyRecords(recorder.Records())
	if len(records) == 0 {
		t.Fatalf("no comp=harvestpy records: %s", recorder.Raw())
	}
	exit := records[len(records)-1]
	if exit.Message != "harvestpy.exit" {
		t.Fatalf("last record = %q, want harvestpy.exit", exit.Message)
	}
	if code, found := exit.Field(obs.FieldExit); !found || code != float64(1) {
		t.Fatalf("a command that exited 1 was recorded as exit=%v (found %t), want 1: %v", code, found, exit.Fields)
	}
	if exit.Level != slog.LevelWarn.String() {
		t.Fatalf("a completed non-zero exit logged at %s, want WARN: %v", exit.Level, exit.Fields)
	}
}

// shrinkDownloadCeiling swaps the download ceiling for the life of one test.
func shrinkDownloadCeiling(t *testing.T, ceiling time.Duration) {
	t.Helper()
	was := downloadCeiling
	downloadCeiling = ceiling
	t.Cleanup(func() { downloadCeiling = was })
}

// TestDownloadFileStopsAtItsCeiling is L2-F22: downloadFile ran on a
// zero-Timeout client with no deadline anywhere in installer.Run →
// installHarvest → Provision, so a server that answered its headers and then
// stalled mid-body hung `pfm install` forever.
func TestDownloadFileStopsAtItsCeiling(t *testing.T) {
	shrinkDownloadCeiling(t, 100*time.Millisecond)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	err := downloadFile(context.Background(), server.URL, filepath.Join(t.TempDir(), "artifact"), 0)
	if err == nil {
		t.Fatal("a download whose server stalled mid-body returned no error")
	}
	if !strings.Contains(err.Error(), "download ceiling") {
		t.Fatalf("downloadFile() error = %v, want it to name the ceiling it stopped at", err)
	}
}

// TestDownloadFileReportsACallerCancellationAsItself: the ceiling's message
// must not be pinned on a caller who cancelled — two different failures.
func TestDownloadFileReportsACallerCancellationAsItself(t *testing.T) {
	shrinkDownloadCeiling(t, time.Minute)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- downloadFile(ctx, server.URL, filepath.Join(t.TempDir(), "artifact"), 0)
	}()
	cancel()
	err := <-done
	if err == nil {
		t.Fatal("a cancelled download returned no error")
	}
	if strings.Contains(err.Error(), "download ceiling") {
		t.Fatalf("a caller cancellation was reported as the download ceiling: %v", err)
	}
}
