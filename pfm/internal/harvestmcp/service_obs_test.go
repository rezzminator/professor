package harvestmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// connectHarvesterInProcess mirrors listToolNames's transport setup (service_test.go)
// but hands back the live client session for a CallTool/GetPrompt round trip.
func connectHarvesterInProcess(t *testing.T, service *Service) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := serverSession.Close(); err != nil {
			t.Errorf("close serverSession: %v", err)
		}
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})
	return session
}

// TestRegisteredToolsRecordUnderTheMCPComponent proves every mcp.AddTool
// registration in register() is wrapped by obs.Tool (items 4/6 of the
// wiring): a real in-process read call writes exactly one mcp.call
// record under the mcp component, tool and kind named, never the pattern.
func TestRegisteredToolsRecordUnderTheMCPComponent(t *testing.T) {
	service, err := NewConfiguredHarvester(
		"test",
		Runtime{Home: t.TempDir(), CacheDir: filepath.Join(t.TempDir(), "cache")},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	_, recorder := obs.Test(t)
	session := connectHarvesterInProcess(t, service)
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      toolRead,
		Arguments: ReadInput{URLs: []string{"doi:10.1000/no-such-needle-MARKER"}},
	}); err != nil {
		t.Fatal(err)
	}
	var found *obs.Record
	for _, record := range recorder.Records() {
		if record.Message == "mcp.call" {
			found = &record
		}
	}
	if found == nil {
		t.Fatalf("no mcp.call record: %s", recorder.Raw())
	}
	for key, want := range map[string]any{obs.FieldComp: "mcp", "kind": "tool", "tool": toolRead} {
		if got, _ := found.Field(key); got != want {
			t.Fatalf("mcp.call record %s = %v, want %v: %v", key, got, want, found.Fields)
		}
	}
	if strings.Contains(recorder.Raw(), "no-such-needle-MARKER") {
		t.Fatalf("the search pattern reached the activity log: %s", recorder.Raw())
	}
}

// TestNewHTTPHandlerWritesAnHTTPInRecord proves NewHTTPHandler's obs.Handler
// wrap (item 6): mcpserv's twin wiring is TestNewHTTPHandlerWritesAnHTTPInRecord
// in internal/mcpserv/httpserv_test.go.
func TestNewHTTPHandlerWritesAnHTTPInRecord(t *testing.T) {
	service, err := NewConfiguredHarvester("test", Runtime{Home: t.TempDir(), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	_, recorder := obs.Test(t)
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/mcp?token=INBOUNDSECRET",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
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
		obs.FieldComp: "http.in", "method": http.MethodPost, "route": "harvester-mcp", "path": "/mcp",
		"status": float64(200),
	} {
		if got, _ := inbound.Field(key); got != want {
			t.Fatalf("http.in record %s = %v, want %v: %v", key, got, want, inbound.Fields)
		}
	}
	if strings.Contains(recorder.Raw(), "INBOUNDSECRET") {
		t.Fatalf("the query token reached the activity log: %s", recorder.Raw())
	}
}

// TestRemoteHandlerWritesAnHTTPInRecord proves RemoteServer.Handler's
// obs.Handler wrap (item 7): a plain /healthz round trip through the gateway
// writes one http.in.request record under the "harvester-remote" route.
func TestRemoteHandlerWritesAnHTTPInRecord(t *testing.T) {
	base := t.TempDir()
	server, err := NewRemote(RemoteOptions{
		Runtime:     Runtime{Home: base, CacheDir: base + "/cache"},
		PublicURL:   "https://harvester.example.test",
		StaticToken: "example-fixture-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, recorder := obs.Test(t)
	request := httptest.NewRequest(http.MethodGet, "https://harvester.example.test/healthz", http.NoBody)
	request.Host = "harvester.example.test"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
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
		obs.FieldComp: "http.in", "method": http.MethodGet, "route": "harvester-remote", "path": "/healthz",
		"status": float64(200),
	} {
		if got, _ := inbound.Field(key); got != want {
			t.Fatalf("http.in record %s = %v, want %v: %v", key, got, want, inbound.Fields)
		}
	}
}
