package mockengine

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// methodLog records the method of every request a server connection reads.
type methodLog struct {
	mu      sync.Mutex
	methods []string
}

type loggedTransport struct {
	inner mcp.Transport
	log   *methodLog
}

func (transport loggedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := transport.inner.Connect(ctx)
	return loggedConnection{connection, transport.log}, err
}

type loggedConnection struct {
	mcp.Connection
	log *methodLog
}

func (connection loggedConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := connection.Connection.Read(ctx)
	if request, ok := message.(*jsonrpc.Request); ok && request.ID.IsValid() {
		connection.log.mu.Lock()
		connection.log.methods = append(connection.log.methods, request.Method)
		connection.log.mu.Unlock()
	}
	return message, err
}

// TestDiscoveringTransportProbesBeforeInitialize pins Claude's MCP opening: server/discover reaches the
// server first, its answer is read, and the handshake and a tools/call still go through; a server that
// never answers the probe fails the connection when its context ends.
func TestDiscoveringTransportProbesBeforeInitialize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping"}, func(context.Context, *mcp.CallToolRequest, struct{}) (
		*mcp.CallToolResult, any, error,
	) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})
	clientSide, serverSide := mcp.NewInMemoryTransports()
	log := &methodLog{}
	serving, err := server.Connect(ctx, loggedTransport{serverSide, log}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serving.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "mock-engine"}, nil)
	session, err := client.Connect(ctx, discoveringTransport{clientSide}, claudeMCPHandshake)
	if err != nil {
		t.Fatalf("connect after the probe: %v", err)
	}
	defer func() { _ = session.Close() }()
	answer, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	if err != nil || len(answer.Content) != 1 || answer.Content[0].(*mcp.TextContent).Text != "pong" {
		t.Fatalf("tools/call after the probe = %+v, %v; want pong", answer, err)
	}
	log.mu.Lock()
	got := slices.Clone(log.methods)
	log.mu.Unlock()
	if want := []string{claudeDiscoverMethod, "initialize", "tools/call"}; !slices.Equal(got, want) {
		t.Fatalf("server read requests %q, want %q", got, want)
	}

	silent, deaf := mcp.NewInMemoryTransports()
	reader, err := deaf.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	go func() {
		for {
			if _, err := reader.Read(ctx); err != nil {
				return
			}
		}
	}()
	short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if _, err := (discoveringTransport{silent}).Connect(short); err == nil {
		t.Fatal("a server that never answered the probe connected")
	}
}
