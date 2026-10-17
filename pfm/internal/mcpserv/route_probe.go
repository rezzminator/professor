package mcpserv

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// probeFamilyRoute catches old daemons whose status advertises the family but
// whose routes predate its current path. A sessionless ping exercises the real
// MCP route without initializing a persistent session or provoking HTTP 400.
// Use the daemon probe bound, preserving any earlier caller deadline.
func probeFamilyRoute(ctx context.Context, address, family string) error {
	ctx, cancel := context.WithTimeout(ctx, daemonProbeTimeout)
	defer cancel()
	endpoint := "http://" + address + pfmconfig.MCPFamilyPath(family)
	const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	request, err := http.NewRequestWithContext(obs.Presence(ctx), http.MethodPost, endpoint, strings.NewReader(ping))
	if err != nil {
		return fmt.Errorf("build family route probe for %s: %w", endpoint, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := obs.WrapClient(&http.Client{}).Do(request)
	if err != nil {
		return fmt.Errorf("probe family route %s: %w", endpoint, err)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		return fmt.Errorf("close family route probe response from %s: %w", endpoint, closeErr)
	}
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s answered HTTP 404", endpoint)
	}
	return nil
}
