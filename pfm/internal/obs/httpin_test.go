package obs

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerRecordsMethodRouteStatusBytesAndDurationNeverTheQueryOrHeaders(t *testing.T) {
	ctx, recorder := Test(t)
	inner := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			t.Errorf("drain body: %v", err)
		}
		writer.WriteHeader(http.StatusAccepted)
		if _, err := io.WriteString(writer, "seven b"); err != nil {
			t.Errorf("write: %v", err)
		}
	})
	request := httptest.NewRequestWithContext(
		ctx, http.MethodPost, "/mcp?token=INBOUNDSECRET", strings.NewReader(`{"prompt":"sk-INBODY"}`),
	)
	request.Header.Set("Authorization", "Bearer INHEADER9")
	response := httptest.NewRecorder()
	Handler("chat-mcp", inner).ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.String() != "seven b" {
		t.Fatalf("the wrapper changed the response: %d %q", response.Code, response.Body.String())
	}

	record := onlyRecord(t, recorder)
	if record.Message != "http.in.request" || record.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s %s, want INFO http.in.request", record.Level, record.Message)
	}
	wantField(t, record, FieldComp, "http.in")
	wantField(t, record, "op", "request")
	wantField(t, record, "method", http.MethodPost)
	wantField(t, record, "route", "chat-mcp")
	wantField(t, record, "path", "/mcp")
	wantField(t, record, "status", float64(http.StatusAccepted))
	wantField(t, record, "bytes", float64(len("seven b")))
	if _, found := record.Field(FieldDur); !found {
		t.Fatalf("record carries no %s: %v", FieldDur, record.Fields)
	}
	for _, secret := range []string{"INBOUNDSECRET", "INHEADER9", "INBODY", "token="} {
		if strings.Contains(recorder.Raw(), secret) {
			t.Fatalf("%q reached the activity log: %s", secret, recorder.Raw())
		}
	}
}

func TestHandlerLevelsFollowTheStatusAndAnUnwrittenResponseIs200(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inner  http.HandlerFunc
		status int
		level  slog.Level
	}{
		{"implicit 200", func(http.ResponseWriter, *http.Request) {}, http.StatusOK, slog.LevelInfo},
		{"write sets 200", func(writer http.ResponseWriter, _ *http.Request) {
			if _, err := io.WriteString(writer, "x"); err != nil {
				t.Errorf("write: %v", err)
			}
		}, http.StatusOK, slog.LevelInfo},
		{"client error warns", func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNotFound)
		}, http.StatusNotFound, slog.LevelWarn},
		{"server error errors", func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusBadGateway)
		}, http.StatusBadGateway, slog.LevelError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := Test(t)
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/status", http.NoBody)
			Handler("probe", tc.inner).ServeHTTP(response, request)
			record := onlyRecord(t, recorder)
			wantField(t, record, "status", float64(tc.status))
			if record.Level != tc.level.String() {
				t.Fatalf("status %d logged at %s, want %s", tc.status, record.Level, tc.level)
			}
		})
	}
}

// TestHandlerKeepsFlushAndUnwrapForStreamingResponses pins what the MCP
// streamable transport needs: it type-asserts http.Flusher on the writer it
// is handed and flushes every SSE event, so a wrapper without Flush would
// silently turn streaming into buffering.
func TestHandlerKeepsFlushAndUnwrapForStreamingResponses(t *testing.T) {
	ctx, recorder := Test(t)
	flushed := false
	inner := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Fatalf("%T does not implement http.Flusher", writer)
		}
		flusher.Flush()
		if err := http.NewResponseController(writer).Flush(); err != nil {
			t.Fatalf("ResponseController.Flush through Unwrap: %v", err)
		}
		if _, err := io.WriteString(writer, "event: ping\n\n"); err != nil {
			t.Errorf("write: %v", err)
		}
		flushed = true
	})
	response := httptest.NewRecorder()
	Handler("stream", inner).ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/mcp", nil))
	if !flushed || !response.Flushed {
		t.Fatalf("flush did not reach the writer: handler ran %t, recorder flushed %t", flushed, response.Flushed)
	}
	wantField(t, onlyRecord(t, recorder), "bytes", float64(len("event: ping\n\n")))
}
