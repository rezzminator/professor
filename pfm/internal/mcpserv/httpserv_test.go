package mcpserv

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
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
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()

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

// TestNewHTTPHandlerWritesAnHTTPInRecord pins the http.in door: the chat MCP
// handler chain logs one record per request with comp=http.in (spec
// § Middleware), never the body it carried.
func TestNewHTTPHandlerWritesAnHTTPInRecord(t *testing.T) {
	setupBackendFixture(t)
	_, recorder := obs.Test(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/mcp?token=INBOUNDSECRET",
		strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"sk-BODYSECRET"}}}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	service.NewHTTPHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var inbound *obs.Record
	for _, record := range recorder.Records() {
		if record.Message == "http.in.request" {
			inbound = &record
		}
	}
	if inbound == nil {
		t.Fatalf("no http.in.request record: %s", recorder.Raw())
	}
	for key, want := range map[string]any{
		obs.FieldComp: "http.in", "method": http.MethodPost, "route": "chat-mcp", "path": "/mcp",
		"status": float64(200),
	} {
		if got, _ := inbound.Field(key); got != want {
			t.Fatalf("http.in record %s = %v, want %v: %v", key, got, want, inbound.Fields)
		}
	}
	if got, found := inbound.Field("bytes"); !found || got.(float64) != float64(response.Body.Len()) {
		t.Fatalf("http.in bytes = %v (found %t), want %d", got, found, response.Body.Len())
	}
	for _, secret := range []string{"INBOUNDSECRET", "BODYSECRET"} {
		if strings.Contains(recorder.Raw(), secret) {
			t.Fatalf("%q reached the activity log: %s", secret, recorder.Raw())
		}
	}
}
