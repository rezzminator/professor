package mcpserv

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// newMCPHTTPHandler exposes server through MCP streamable HTTP. Authentication
// and endpoint routing belong to the process-level daemon, so this handler is
// also useful in httptest and in a caller that mounts it under its own policy.
// Every request leaves one comp=http.in record under route — the http.in door
// of spec § Middleware; the tool call inside logs separately under comp=mcp.
// A harvester tool call's context ends when its request does
// (harvestmcp.LinkClientDisconnect).
func newMCPHTTPHandler(server *mcp.Server, route string) http.Handler {
	return obs.Handler(route, harvestmcp.LinkClientDisconnect(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			JSONResponse:               true,
			Stateless:                  false,
			DisableLocalhostProtection: false,
		},
	)))
}
