package mcpserv

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hostops/pfm/internal/obs"
)

// NewHTTPHandler exposes the same server and tool registrations as the stdio
// transport through MCP streamable HTTP. Authentication and endpoint routing
// belong to the process-level daemon, so this handler is also useful in
// httptest and in a caller that mounts it under its own policy. Every request
// leaves one comp=http.in record (route chat-mcp) — the http.in door of spec
// § Middleware; the tool call inside logs separately under comp=mcp.
func (service *Service) NewHTTPHandler() http.Handler {
	return obs.Handler("chat-mcp", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return service.server },
		&mcp.StreamableHTTPOptions{
			JSONResponse:               true,
			Stateless:                  false,
			DisableLocalhostProtection: false,
		},
	))
}
