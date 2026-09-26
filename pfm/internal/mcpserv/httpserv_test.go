package mcpserv

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// professorHandlers is every handler newMCPHTTPHandler builds for a Professor
// carrying both families, keyed by the obs route each one logs under.
func professorHandlers(t *testing.T) map[string]http.Handler {
	t.Helper()
	professor := newTestProfessor(t, ProfessorOptions{
		Chat: newTestChat(t), Harvester: newTestHarvester(t, harvestmcp.Runtime{}),
	})
	return map[string]http.Handler{
		"professor-mcp":           professor.Handler(),
		"professor-mcp-chat":      professor.FamilyHandler(pfmconfig.MCPServerChat),
		"professor-mcp-harvester": professor.FamilyHandler(pfmconfig.MCPServerHarvester),
	}
}

func TestProfessorHandlersServeStreamableMCP(t *testing.T) {
	for route, handler := range professorHandlers(t) {
		request := httptest.NewRequest(
			http.MethodPost,
			"http://127.0.0.1/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200; body=%s", route, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"serverInfo"`) {
			t.Fatalf("%s initialize response = %s", route, recorder.Body.String())
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
		handler.ServeHTTP(recorder, foreign)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf(
				"%s foreign Host status=%d, want localhost protection refusal; body=%s",
				route,
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}
}

// TestProfessorHandlersWriteAnHTTPInRecord pins the http.in door: every
// professor handler chain logs one record per request with comp=http.in (spec
// § Middleware) under its own route, never the body it carried.
func TestProfessorHandlersWriteAnHTTPInRecord(t *testing.T) {
	handlers := professorHandlers(t)
	for route, handler := range handlers {
		t.Run(route, func(t *testing.T) {
			_, recorder := obs.Test(t)
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
			handler.ServeHTTP(response, request)
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
				obs.FieldComp: "http.in", "method": http.MethodPost, "route": route, "path": "/mcp",
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
		})
	}
}
