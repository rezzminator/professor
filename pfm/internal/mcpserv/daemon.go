package mcpserv

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// mcpProtocolVersion is the MCP revision the daemon reports on /status.
const mcpProtocolVersion = "2025-06-18"

// daemonRoute labels the daemon's own door in the activity log, beside the
// route each mounted handler logs under (chat-professor).
const daemonRoute = "mcp-daemon"

// maxDaemonBodyBytes bounds every request body the daemon accepts, on the
// MCP routes and /status alike. The MCP streamable transport under these
// routes reads the whole body into memory before it parses anything, so an
// unbounded POST can exhaust the daemon; 1 MiB exceeds every chat input.
const maxDaemonBodyBytes = 1 << 20

// DaemonOptions is everything `pfm mcp serve` hands the loopback daemon's
// front door. Chat is served at config.MCPFamilyPath. A nil view
// means that family is disabled by config and was never constructed.
type DaemonOptions struct {
	Version             string
	StartedAt           time.Time
	Endpoint            string
	Chat                http.Handler
	ChatRuntimeIdentity string
	// Clock defaults StartedAt when it is left zero; nil reads the wall
	// clock exactly as an unset StartedAt always has.
	Clock clock.Clock
	// Warnings receives the daemon's own non-fatal failures — a status
	// document that could not be encoded. nil sends them to stderr, which is
	// where a daemon started from a shell is read.
	Warnings io.Writer
}

// NewDaemonHandler builds the loopback daemon's front door: /status plus one
// route per mounted MCP server.
//
// The trust boundary here is the loopback bind and nothing else. This daemon
// authenticates no caller (`pfm install` registers every client without a
// credential — internal/installer/mcp.go), so every local process and every
// local user that can open 127.0.0.1 can call the chat tools this mounts.
// The Origin refusal below stops a browser, not a peer. Every engine
// registers `pfm mcp serve --stdio`, and each stdio server forwards to this
// daemon's config.MCPFamilyPath(config.MCPServerChat) route, so every local client reaches the
// chat tools through this daemon; narrowing it to the owning user needs a
// per-daemon secret in every client registration, which is an installer-side
// change.
func NewDaemonHandler(options DaemonOptions) http.Handler {
	if options.Clock == nil {
		options.Clock = clock.Real
	}
	if options.StartedAt.IsZero() {
		options.StartedAt = options.Clock.Now().UTC()
	}
	// Servers reports only what is actually mounted below, never the full
	// registered set: mcp.servers.<name>.enabled=false means the handler was
	// never constructed, and /status must not claim a tool surface the
	// daemon cannot serve.
	servers := map[string][]string{}
	if options.Chat != nil {
		servers[pfmconfig.MCPServerChat] = ToolNames()
	} else {
		options.ChatRuntimeIdentity = ""
	}
	status := DaemonStatus{
		PFMVersion:          options.Version,
		ProtocolVersion:     mcpProtocolVersion,
		Servers:             servers,
		PID:                 os.Getpid(),
		StartTime:           options.StartedAt.UTC().Format(time.RFC3339Nano),
		Endpoint:            options.Endpoint,
		ChatRuntimeIdentity: options.ChatRuntimeIdentity,
	}
	// obs.Handler wraps the OUTER handler, so /status, a disabled route's 503,
	// an unknown path's 404 and the Origin refusal — the answers the daemon
	// gives itself, which no mounted server ever sees — each leave their own
	// http.in record under this route. A request that does reach a mounted
	// server is recorded twice on purpose: once here, for what the daemon's
	// door answered, and once by the chat handler, for what its server did.
	return obs.Handler(daemonRoute, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ContentLength > maxDaemonBodyBytes {
			http.Error(
				writer,
				fmt.Sprintf("request body is %d bytes; the limit is %d", request.ContentLength, maxDaemonBodyBytes),
				http.StatusRequestEntityTooLarge,
			)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxDaemonBodyBytes)
		// Browsers attach Origin even when script code targets loopback. Local
		// MCP clients do not. Refuse browser-capable cross-origin requests before
		// they can reach call or any other mounted tool.
		if request.Header.Get("Origin") != "" {
			http.Error(writer, "browser-origin requests are forbidden", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/status":
			if request.Method != http.MethodGet {
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			current := status
			writeDaemonJSON(writer, options.Warnings, current)
		case pfmconfig.MCPFamilyPath(pfmconfig.MCPServerChat):
			serveDaemonRoute(writer, request, options.Chat, pfmconfig.MCPServerChat)
		default:
			if request.URL.Path == "/mcp/professor" || strings.HasPrefix(request.URL.Path, "/mcp/professor/") {
				http.Error(writer, fmt.Sprintf(
					"pfm mcp: /mcp/professor is retired; chat MCP moved to %s; restart the chat to reconnect with the current MCP registrations",
					pfmconfig.MCPFamilyPath(pfmconfig.MCPServerChat),
				), http.StatusGone)
				return
			}
			http.NotFound(writer, request)
		}
	}))
}

// serveDaemonRoute dispatches to a mounted server's handler. handler is
// nil exactly when mcp.servers.<name>.enabled is false, in which case a
// plain 404 would be indistinguishable from the daemon not running at all;
// answer with an explicit refusal instead so a disabled route reads as
// disabled, not broken.
func serveDaemonRoute(writer http.ResponseWriter, request *http.Request, handler http.Handler, name string) {
	if handler == nil {
		http.Error(
			writer,
			fmt.Sprintf("pfm mcp: %s is disabled by config; enable it with: pfm mcp %s enable", name, name),
			http.StatusServiceUnavailable,
		)
		return
	}
	handler.ServeHTTP(writer, request)
}

func writeDaemonJSON(writer http.ResponseWriter, warnings io.Writer, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		// The response may already be committed. There is no useful second
		// response to write, but the failure still has to be readable.
		if warnings == nil {
			warnings = os.Stderr
		}
		fmt.Fprintf(warnings, "pfm mcp: encode status: %v\n", err)
	}
}
