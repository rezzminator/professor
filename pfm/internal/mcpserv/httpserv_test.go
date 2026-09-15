package mcpserv

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
)

func TestNewHTTPHandlerServesStreamableMCP(t *testing.T) {
	setupBackendFixture(t)
	service, err := NewConfigured("test", nil, Runtime{
		Paths: func() paths.Values {
			resolved, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			return resolved
		}(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	recorder := httptest.NewRecorder()
	service.NewHTTPHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"serverInfo"`) {
		t.Fatalf("initialize response = %s", recorder.Body.String())
	}
	foreign := httptest.NewRequest(
		http.MethodPost,
		"http://attacker.example/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}`),
	)
	foreign.Header.Set("Content-Type", "application/json")
	foreign.Header.Set("Accept", "application/json, text/event-stream")
	foreign = foreign.WithContext(context.WithValue(
		foreign.Context(),
		http.LocalAddrContextKey,
		&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8377},
	))
	recorder = httptest.NewRecorder()
	service.NewHTTPHandler().ServeHTTP(recorder, foreign)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf(
			"foreign Host status=%d, want localhost protection refusal; body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
}
