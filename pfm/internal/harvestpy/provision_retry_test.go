package harvestpy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

func shrinkDownloadRetryBackoff(t *testing.T) {
	t.Helper()
	was := downloadRetryBackoff
	downloadRetryBackoff = time.Millisecond
	t.Cleanup(func() { downloadRetryBackoff = was })
}

func TestDownloadFileRetriesServerFailure(t *testing.T) {
	shrinkDownloadRetryBackoff(t)
	ctx, recorder := obs.Test(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "try again", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "artifact")
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "artifact")
	if err := downloadFile(ctx, clock.Real, server.URL+"/archive?secret=hidden", path, 8); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "artifact" {
		t.Fatalf("body = %q, error = %v", body, err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	var retries, second int
	for _, record := range recorder.Records() {
		if record.Message == "harvestpy.download.retry" {
			retries++
			if record.Level != slog.LevelWarn.String() {
				t.Fatalf("retry level = %s", record.Level)
			}
			wantField(t, record, "path", "/archive")
		}
		if record.Message == "http.out.request" {
			second++
			if second == 2 {
				wantField(t, record, "retries", float64(1))
			}
		}
	}
	if retries != 1 || second != 2 || strings.Contains(recorder.Raw(), "hidden") {
		t.Fatalf("records: %s", recorder.Raw())
	}
}

func TestDownloadFileDoesNotRetry404(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.NotFound(w, nil)
	}))
	defer server.Close()
	ctx, recorder := obs.Test(t)
	err := downloadFile(ctx, clock.Real, server.URL, filepath.Join(t.TempDir(), "artifact"), 0)
	if err == nil || !strings.Contains(err.Error(), "download returned HTTP 404 Not Found") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 1 || strings.Contains(recorder.Raw(), "harvestpy.download.retry") {
		t.Fatalf("requests = %d; records = %s", requests.Load(), recorder.Raw())
	}
}

func TestDownloadFileGivesUpAfterServerFailures(t *testing.T) {
	shrinkDownloadRetryBackoff(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	err := downloadFile(context.Background(), clock.Real, server.URL, filepath.Join(t.TempDir(), "artifact"), 0)
	if err == nil ||
		!strings.Contains(err.Error(), "download returned HTTP 503 Service Unavailable") ||
		!strings.Contains(err.Error(), "after 4 attempts") {
		t.Fatalf("error = %v", err)
	}
	if got := requests.Load(); got != downloadAttempts {
		t.Fatalf("requests = %d, want %d", got, downloadAttempts)
	}
}

func TestDownloadFileRetriesDroppedConnection(t *testing.T) {
	shrinkDownloadRetryBackoff(t)
	ctx, recorder := obs.Test(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		_, _ = io.WriteString(w, "artifact")
	}))
	defer server.Close()
	if err := downloadFile(ctx, clock.Real, server.URL, filepath.Join(t.TempDir(), "artifact"), 8); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
	for _, record := range recorder.Records() {
		if record.Message == "http.out.request" {
			if record.Level != slog.LevelWarn.String() {
				t.Fatalf("dropped attempt level = %s", record.Level)
			}
			return
		}
	}
	t.Fatal("no outbound record")
}

func TestDownloadFileDoesNotRetryChecksumMismatch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "wrong")
	}))
	defer server.Close()
	pinned := sha256.Sum256([]byte("right"))
	input := Artifact{URL: server.URL, Size: 5, SHA256: hex.EncodeToString(pinned[:])}
	err := ensureInput(context.Background(), filepath.Join(t.TempDir(), "artifact"), input, false, nil)
	if err == nil || !strings.Contains(err.Error(), "verify downloaded input") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestDownloadFileStopsWhenCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	timing := cancelDownloadClock{Clock: clock.Real, cancel: cancel}
	err := downloadFile(ctx, timing, server.URL, filepath.Join(t.TempDir(), "artifact"), 0)
	if !errors.Is(err, context.Canceled) || requests.Load() != 1 {
		t.Fatalf("error = %v, requests = %d", err, requests.Load())
	}
}

type cancelDownloadClock struct {
	clock.Clock
	cancel context.CancelFunc
}

func (c cancelDownloadClock) Sleep(ctx context.Context, _ time.Duration) error {
	c.cancel()
	return ctx.Err()
}

func TestDownloadFileLastTransportFailureIsError(t *testing.T) {
	shrinkDownloadRetryBackoff(t)
	ctx, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer server.Close()
	err := downloadFile(ctx, clock.Real, server.URL, filepath.Join(t.TempDir(), "artifact"), 0)
	if err == nil || !strings.Contains(err.Error(), "after 4 attempts") {
		t.Fatalf("error = %v", err)
	}
	var outbound []obs.Record
	for _, record := range recorder.Records() {
		if record.Message == "http.out.request" {
			outbound = append(outbound, record)
		}
	}
	if len(outbound) != downloadAttempts || outbound[len(outbound)-1].Level != slog.LevelError.String() {
		t.Fatalf("outbound records = %v", outbound)
	}
}
