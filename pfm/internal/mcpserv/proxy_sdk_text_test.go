package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestProxySessionLostIsOnlyTheSDKSessionText pins pfm-update-6#F5: a 404
// whose body merely mentions "session" is the daemon refusing the request,
// never a lost session — recovery (re-initialize and retry) keys off the
// go-sdk's own session-loss texts only.
func TestProxySessionLostIsOnlyTheSDKSessionText(t *testing.T) {
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	for _, test := range []struct {
		body string
		lost bool
	}{
		{"session not found\n", true},
		{"session is closing\n", true},
		{"404 page not found: /mcp/session-export\n", false},
		{`{"error":"no tool named session_log"}`, false},
		{"404 page not found\n", false},
	} {
		err := proxy.daemonStatusError(http.StatusNotFound, []byte(test.body))
		var lost proxySessionLostError
		if got := errors.As(err, &lost); got != test.lost {
			t.Errorf("404 %q classified lost=%v, want %v (%v)", test.body, got, test.lost, err)
		}
	}
}

// TestProxyRecoveryTextsMatchTheGoSDK pins pfm-update-6#F6: the proxy's
// recovery reads two go-sdk texts, and this test drives the real sdk so a
// changed text fails here, not silently in a proxy that stops recovering.
func TestProxyRecoveryTextsMatchTheGoSDK(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "pin", Version: "test"}, nil)

	// The streamable handler's 404 for a session it does not hold.
	daemon := httptest.NewServer(newMCPHTTPHandler(server, "pin"))
	defer daemon.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, daemon.URL,
		strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Session-Id", "gone")
	response, err := daemon.Client().Do(request)
	if err != nil {
		t.Fatalf("post to the sdk handler: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	proxy := newStdioProxy(ctx, "unused", io.Discard)
	var lost proxySessionLostError
	if err := proxy.daemonStatusError(response.StatusCode, body); !errors.As(err, &lost) {
		t.Fatalf("the go-sdk's unknown-session answer HTTP %d %q is not classified lost: %v",
			response.StatusCode, body, err)
	}
	if !strings.Contains(string(body), mcp.ErrSessionMissing.Error()) {
		t.Fatalf("the go-sdk 404 body %q no longer carries mcp.ErrSessionMissing %q", body, mcp.ErrSessionMissing)
	}

	// The server session's answer to a request before initialize.
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	session, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	conn, err := clientTransport.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	id, err := jsonrpc.MakeID(float64(2))
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, &jsonrpc.Request{
		ID: id, Method: "tools/call", Params: json.RawMessage(`{"name":"chat_status"}`),
	}); err != nil {
		t.Fatal(err)
	}
	message, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonrpc.EncodeMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	if !uninitializedSessionResponse(encoded) {
		t.Fatalf("the go-sdk's pre-initialize answer %s is not recognized as an uninitialized session", encoded)
	}
}
