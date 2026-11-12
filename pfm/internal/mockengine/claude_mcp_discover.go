package mockengine

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// claudeDiscoverMethod is the probe Claude Code (2.1.281 on) sends a stdio MCP server before initialize.
const claudeDiscoverMethod = "server/discover"

// claudeMCPHandshake is the protocol Claude Code initializes its MCP servers with. A client asking for
// the SDK's newest protocol (2026-07-28) sends the stateless server/discover itself and never
// initializes; Claude probes, then initializes whatever the probe answered.
var claudeMCPHandshake = &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"}

// discoveringTransport opens an MCP connection as Claude Code does: the server/discover probe sent and
// its answer read before the handshake, which goes on whatever the server answered. A server that
// never answers the probe fails the connection when the context ends.
type discoveringTransport struct {
	inner mcp.Transport
}

func (transport discoveringTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := transport.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := probeDiscover(ctx, connection); err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	return connection, nil
}

// probeDiscover sends the probe and reads up to its answer.
func probeDiscover(ctx context.Context, connection mcp.Connection) error {
	id, err := jsonrpc.MakeID("discover")
	if err != nil {
		return fmt.Errorf("make the %s id: %w", claudeDiscoverMethod, err)
	}
	if err := connection.Write(ctx, &jsonrpc.Request{ID: id, Method: claudeDiscoverMethod}); err != nil {
		return fmt.Errorf("send %s: %w", claudeDiscoverMethod, err)
	}
	for {
		message, err := connection.Read(ctx)
		if err != nil {
			return fmt.Errorf("read the answer to %s: %w", claudeDiscoverMethod, err)
		}
		if response, ok := message.(*jsonrpc.Response); ok && response.ID == id {
			return nil
		}
	}
}
