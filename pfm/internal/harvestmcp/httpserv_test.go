package harvestmcp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHTTPHandlerServesStreamableMCP(t *testing.T) {
	service, err := NewConfigured("test", Runtime{Home: t.TempDir(), CacheDir: t.TempDir()})
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

func TestResolverClientRejectsPrivateRedirects(t *testing.T) {
	client, err := newHTTPClient(Runtime{})
	if err != nil {
		t.Fatal(err)
	}
	if client.CheckRedirect == nil {
		t.Fatal("resolver client has no redirect SSRF guard")
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/private", http.NoBody)
	if err := client.CheckRedirect(request, nil); err == nil {
		t.Fatal("resolver client allowed redirect to loopback")
	}
}
