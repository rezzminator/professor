package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	PFMVersion        string              `json:"pfmVersion"`
	ProtocolVersion   string              `json:"protocolVersion"`
	Servers           map[string][]string `json:"servers"`
	PID               int                 `json:"pid"`
	StartTime         string              `json:"startTime"`
	Endpoint          string              `json:"endpoint"`
	HarvesterExternal string              `json:"harvesterExternal,omitempty"`
}

// ErrDaemonAbsent means nothing answered on the probed address at all: the
// only probe outcome that says the port is free for `pfm mcp serve` to bind.
// A transport failure on loopback is a refused connection, so it is reported
// as absence WITH its own cause attached; if something is in fact listening
// and merely too slow for the probe's deadline, the bind that follows fails
// with the kernel's own address-in-use refusal rather than silently
// succeeding. Every other failure below means something IS holding the port
// and did not identify itself as pfm's daemon.
var ErrDaemonAbsent = errors.New("no service answered")

// ProbeDaemon reads a healthy loopback daemon's status document, and names
// which way the probe failed when it did not: absent (nothing listening), a
// non-200 answer, a body that is not the status document, or a document
// carrying no pid. A caller deciding whether to bind the port must be able to
// tell "nothing is there" from "something else is" — collapsing all four into
// one false made a foreign service on pfm's port read as a free port.
func ProbeDaemon(address string) (DaemonStatus, error) {
	endpoint := "http://" + address + "/status"
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("build daemon probe for %s: %w", endpoint, err)
	}
	client := obs.WrapClient(&http.Client{Timeout: 300 * time.Millisecond})
	response, err := client.Do(request)
	if err != nil {
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
