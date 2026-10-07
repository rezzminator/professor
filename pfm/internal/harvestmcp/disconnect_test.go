package harvestmcp

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpPost sends one JSON-RPC body to endpoint over streamable HTTP under ctx,
// carrying session when set, and answers the response (nil when ctx ended).
func mcpPost(ctx context.Context, t *testing.T, endpoint, session, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		request.Header.Set("Mcp-Session-Id", session)
		request.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		t.Fatal(err)
	}
	return response
}

// TestHarvesterToolStopsWhenTheClientDisconnects: an MCP client that drops
// its HTTP request mid-call (a killed chat, a closed laptop) sends no cancel
// notification, and the SDK runs the handler on its session's context, not
// the request's — so a conversion ran on for minutes for nobody. The tool's
// context ends, naming the disconnect, as soon as the request does.
func TestHarvesterToolStopsWhenTheClientDisconnects(t *testing.T) {
	t.Parallel()
	started, gone, release := make(chan struct{}), make(chan error, 1), make(chan struct{})
	t.Cleanup(func() { close(release) })
	server := mcp.NewServer(&mcp.Implementation{Name: "disconnect-test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "block"}, untilClientGone(
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			close(started)
			select {
			case <-ctx.Done():
				gone <- context.Cause(ctx)
			case <-release:
			}
			return nil, struct{}{}, ctx.Err()
		}))
	httpServer := httptest.NewServer(LinkClientDisconnect(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})))
	t.Cleanup(httpServer.Close)

	initialize := mcpPost(context.Background(), t, httpServer.URL, "",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",`+
			`"capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	session := initialize.Header.Get("Mcp-Session-Id")
	_ = initialize.Body.Close()
	if session == "" {
		t.Fatalf("initialize answered %s with no session", initialize.Status)
	}
	initialized := mcpPost(context.Background(), t, httpServer.URL, session,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	_ = initialized.Body.Close()

	callCtx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	go func() {
		if response := mcpPost(
			callCtx,
			t,
			httpServer.URL,
			session,
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"block","arguments":{}}}`,
		); response != nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool call never reached the handler")
	}
	disconnect()
	select {
	case cause := <-gone:
		if !errors.Is(cause, errClientGone) {
			t.Fatalf("tool context ended with %v, want errClientGone", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tool's context outlived the client's disconnect")
	}
}
