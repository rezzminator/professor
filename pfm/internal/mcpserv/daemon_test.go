package mcpserv

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestDaemonHandlerBoundsRequestBodies pins the memory bound on the loopback
// daemon's own routes: the MCP transport under them reads the whole body into
// memory, so an unbounded POST from any local process is a one-request
// out-of-memory on the process that serves every chat on the machine. A body
// inside the bound must still arrive whole — an inject prompt is legitimate
// input and must not be clipped.
func TestDaemonHandlerBoundsRequestBodies(t *testing.T) {
	var readErr error
	var readBytes int
	mounted := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		readErr, readBytes = err, len(body)
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := NewDaemonHandler(DaemonOptions{Chat: mounted, Harvester: mounted})

	legitimate := bytes.Repeat([]byte("p"), maxDaemonBodyBytes/2)
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/mcp/chat", bytes.NewReader(legitimate)),
	)
	if readErr != nil || readBytes != len(legitimate) {
		t.Fatalf(
			"a %d-byte tool input read as %d bytes (err=%v); the bound must not clip legitimate input",
			len(legitimate), readBytes, readErr,
		)
	}

	for _, route := range []string{"/mcp/chat", "/mcp/harvester"} {
		t.Run(route, func(t *testing.T) {
			readErr, readBytes = nil, 0
			oversized := bytes.Repeat([]byte("x"), maxDaemonBodyBytes+4096)
			// A streamed body declares no length, so only the reader can bound it.
			streamed := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(oversized))
			streamed.ContentLength = -1
			handler.ServeHTTP(httptest.NewRecorder(), streamed)
			var bound *http.MaxBytesError
			if !errors.As(readErr, &bound) {
				t.Fatalf(
					"%s read %d bytes with err=%v; want the read refused at %d bytes",
					route, readBytes, readErr, maxDaemonBodyBytes,
				)
			}
			if readBytes > maxDaemonBodyBytes {
				t.Fatalf("%s buffered %d bytes past the %d-byte bound", route, readBytes, maxDaemonBodyBytes)
			}
		})
	}
}

// TestDaemonHandlerLeavesAnHTTPInRecordForEveryAnswerItGivesItself pins the
// daemon's own door on the activity log: /status, a disabled route's 503, an
// unknown path's 404 and the browser-origin refusal are all answered by the
// daemon handler itself, so without the middleware around it they were the
// only HTTP answers on this process that left no record at all.
func TestDaemonHandlerLeavesAnHTTPInRecordForEveryAnswerItGivesItself(t *testing.T) {
	ctx, recorder := obs.Test(t)
	handler := NewDaemonHandler(DaemonOptions{Version: "test"})
	origin := httptest.NewRequestWithContext(ctx, http.MethodPost, "/mcp/chat", http.NoBody)
	origin.Header.Set("Origin", "https://attacker.example")
	for _, request := range []*http.Request{
		httptest.NewRequestWithContext(ctx, http.MethodGet, "/status", http.NoBody),
		httptest.NewRequestWithContext(ctx, http.MethodPost, "/mcp/chat", http.NoBody),
		httptest.NewRequestWithContext(ctx, http.MethodGet, "/nothing-here", http.NoBody),
		origin,
	} {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	var statuses []float64
	for _, record := range recorder.Records() {
		if record.Message != "http.in.request" {
			continue
		}
		route, _ := record.Field("route")
		if route != daemonRoute {
			t.Fatalf("http.in record route = %v, want %q: %v", route, daemonRoute, record.Fields)
		}
		status, _ := record.Field("status")
		value, ok := status.(float64)
		if !ok {
			t.Fatalf("http.in record carries no status: %v", record.Fields)
		}
		statuses = append(statuses, value)
	}
	want := []float64{
		http.StatusOK,
		http.StatusServiceUnavailable,
		http.StatusNotFound,
		http.StatusForbidden,
	}
	if len(statuses) != len(want) {
		t.Fatalf("daemon answers recorded = %v, want one record per answer %v: %s", statuses, want, recorder.Raw())
	}
	for index, status := range want {
		if statuses[index] != status {
			t.Fatalf("recorded statuses = %v, want %v: %s", statuses, want, recorder.Raw())
		}
	}
	if strings.Contains(recorder.Raw(), "attacker.example") {
		t.Fatalf("the refused Origin header reached the activity log: %s", recorder.Raw())
	}
}

// A body that DECLARES a length over the bound is refused by name before any
// mounted server reads it: the MCP transport underneath answers a clipped read
// with a bare "failed to read body", which tells the caller nothing.
func TestDaemonHandlerNamesADeclaredOversizedBody(t *testing.T) {
	reached := false
	mounted := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
	handler := NewDaemonHandler(DaemonOptions{Chat: mounted, Harvester: mounted})
	for _, route := range []string{"/mcp/chat", "/mcp/harvester"} {
		reached = false
		recorder := httptest.NewRecorder()
		oversized := bytes.Repeat([]byte("x"), maxDaemonBodyBytes+1)
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, route, bytes.NewReader(oversized)))
		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s answered %d, want 413", route, recorder.Code)
		}
		if body := recorder.Body.String(); !strings.Contains(body, "request body") ||
			!strings.Contains(body, strconv.Itoa(maxDaemonBodyBytes)) {
			t.Fatalf("%s refusal does not name the limit: %q", route, body)
		}
		if reached {
			t.Fatalf("%s handed a declared-oversized body to the mounted server", route)
		}
	}
}
