package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// DaemonStatus is the stable local health document consumed by doctor and the
// single-instance probe.
type DaemonStatus struct {
	PFMVersion          string              `json:"pfmVersion"`
	ProtocolVersion     string              `json:"protocolVersion"`
	Servers             map[string][]string `json:"servers"`
	PID                 int                 `json:"pid"`
	StartTime           string              `json:"startTime"`
	Endpoint            string              `json:"endpoint"`
	HarvesterExternal   string              `json:"harvesterExternal,omitempty"`
	ChatRuntimeIdentity string              `json:"chatRuntimeIdentity,omitempty"`
}

// ErrDaemonAbsent means the connection failed without timing out. It is the
// only probe outcome that lets `pfm mcp serve` try to bind the port.
var ErrDaemonAbsent = errors.New("no service answered")

// ErrDaemonUnresponsive means a listener held the port but did not answer the
// status probe before its deadline. Callers must not try to bind that port.
var ErrDaemonUnresponsive = errors.New("no status answer within the probe deadline")

const daemonProbeTimeout = 2 * time.Second

// DaemonProbeTimeoutOverride replaces the production probe deadline under
// test when positive. Zero leaves the production deadline in effect.
var DaemonProbeTimeoutOverride time.Duration

// ProbeDaemon reads a healthy loopback daemon's status document, and names
// which way the probe failed when it did not: absent (connection failed),
// unresponsive (deadline reached), a non-200 answer, a body that is not the
// status document, or a document carrying no pid.
func ProbeDaemon(address string) (DaemonStatus, error) {
	return probeDaemonContext(context.Background(), address)
}

// probeDaemonContext is ProbeDaemon under the caller's context: a cancelled or
// expired caller returns its own context error, never absent or unresponsive.
func probeDaemonContext(ctx context.Context, address string) (DaemonStatus, error) {
	endpoint := "http://" + address + "/status"
	request, err := http.NewRequestWithContext(
		obs.Presence(ctx),
		http.MethodGet,
		endpoint,
		http.NoBody,
	)
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("build daemon probe for %s: %w", endpoint, err)
	}
	deadline := daemonProbeTimeout
	if DaemonProbeTimeoutOverride > 0 {
		deadline = DaemonProbeTimeoutOverride
	}
	client := obs.WrapClient(&http.Client{Timeout: deadline})
	response, err := client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return DaemonStatus{}, fmt.Errorf("daemon probe at %s: %w", address, ctxErr)
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return DaemonStatus{}, fmt.Errorf("%w at %s within %s: %w", ErrDaemonUnresponsive, address, deadline, err)
		}
		return DaemonStatus{}, fmt.Errorf("%w at %s: %w", ErrDaemonAbsent, address, err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "pfm mcp serve: close daemon probe response: %v\n", err)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return DaemonStatus{}, fmt.Errorf(
			"%s answered HTTP %d, not pfm's status document",
			endpoint,
			response.StatusCode,
		)
	}
	var status DaemonStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return DaemonStatus{}, fmt.Errorf("%s answered a body that is not pfm's status document: %w", endpoint, err)
	}
	if status.PID < 1 {
		return DaemonStatus{}, fmt.Errorf(
			"%s answered a status document with pid %d, not pfm's daemon",
			endpoint,
			status.PID,
		)
	}
	return status, nil
}

// DaemonReachability probes the configured loopback daemon for doctor. The
// probe's own error travels out whole, so the report says WHICH failure it
// was — a refused connection, a foreign service, a body that did not parse —
// instead of one "unreachable" for every one of them.
func DaemonReachability(runtime config.Runtime) (DaemonStatus, error) {
	return ProbeDaemon("127.0.0.1:" + strconv.Itoa(runtime.Config.MCP.HTTP.Port))
}
