package harvestmcp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// errClientGone is the cause a harvester tool's context ends with when the
// HTTP request its call arrived on ended first: the MCP client disconnected,
// and nobody will read the answer.
var errClientGone = errors.New("the MCP client disconnected before the answer")

// clientRequestHeader carries the daemon's own reference to the HTTP request a
// call arrived on. LinkClientDisconnect stamps it on EVERY request,
// overwriting whatever the client sent, so a client never names another's.
const clientRequestHeader = "X-Pfm-Client-Request"

// clientRequests maps a live request's reference to its context, for the
// length of its ServeHTTP.
var (
	clientRequests   sync.Map
	clientRequestSeq atomic.Uint64
)

// LinkClientDisconnect lets a harvester tool see its client go away. The MCP
// SDK runs a tool handler on its session's context, never the HTTP request's:
// a notifications/cancelled reaches the handler, a dropped connection does
// not. This middleware registers each request's context under a reference it
// stamps into the request's headers, which the SDK hands the tool call as
// RequestExtra.Header; untilClientGone links the two. Every MCP HTTP handler
// carrying the harvester family mounts behind it.
func LinkClientDisconnect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ref := strconv.FormatUint(clientRequestSeq.Add(1), 36)
		request.Header.Set(clientRequestHeader, ref)
		clientRequests.Store(ref, request.Context())
		defer clientRequests.Delete(ref)
		next.ServeHTTP(writer, request)
	})
}

// untilClientGone runs handler under a context that also ends, with cause
// errClientGone, when the HTTP request the call arrived on ends (a call with no
// such request — stdio, in-memory — runs unchanged). A conversion queued or
// running for that call is then cancelled and its worker freed.
func untilClientGone[In, Out any](handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		if request != nil && request.Extra != nil && request.Extra.Header != nil {
			if value, ok := clientRequests.Load(request.Extra.Header.Get(clientRequestHeader)); ok {
				if requestCtx, isCtx := value.(context.Context); isCtx {
					linked, cancel := context.WithCancelCause(ctx)
					stop := context.AfterFunc(requestCtx, func() { cancel(errClientGone) })
					defer func() {
						stop()
						cancel(nil)
					}()
					ctx = linked
				}
			}
		}
		return handler(ctx, request, input)
	}
}
