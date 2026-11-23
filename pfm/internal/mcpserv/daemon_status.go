package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
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
	ChatRuntimeIdentity string              `json:"chatRuntimeIdentity,omitempty"`
}

// ErrDaemonAbsent means the connection failed without timing out. It is the
// only probe outcome that lets `pfm mcp serve` try to bind the port.
var ErrDaemonAbsent = errors.New("no service answered")

// ErrDaemonUnresponsive means the status probe timed out within its 2 s deadline,
// including a dial timeout. Callers must not try to bind that port.
var ErrDaemonUnresponsive = errors.New("no status answer within the probe deadline")

const daemonProbeTimeout = 2 * time.Second

// ProbeDaemon reads a healthy loopback daemon's status document within 2 s.
// Any timeout, including a dial timeout, reads as unresponsive; a connection
// failure without a timeout reads as absent. Other errors name a non-200 answer,
// a body that is not the status document, or a document carrying no pid.
func ProbeDaemon(address string) (DaemonStatus, error) {
	return probeDaemonContext(context.Background(), address)
}

// probeDaemonContext is ProbeDaemon under the caller's context: a cancelled or
// expired caller returns its own context error, never absent or unresponsive.
// Its deadline runs on the process clock, read at the call.
func probeDaemonContext(ctx context.Context, address string) (DaemonStatus, error) {
	return probeDaemonOn(ctx, clock.Real, address)
}

// probeDaemonOn is probeDaemonContext with its deadline on c: the bound
// net/http's Client.Timeout set before it moved onto the clock seam, failing
// with the same text (probeTimeoutText).
func probeDaemonOn(ctx context.Context, c clock.Clock, address string) (DaemonStatus, error) {
	endpoint := "http://" + address + "/status"
	deadline := daemonProbeTimeout
	bounded, cancel := clock.WithTimeout(ctx, c, deadline)
	defer cancel()
	request, err := http.NewRequestWithContext(
		obs.Presence(bounded),
		http.MethodGet,
		endpoint,
		http.NoBody,
	)
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("build daemon probe for %s: %w", endpoint, err)
	}
	client := obs.WrapClient(&http.Client{})
	response, err := client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return DaemonStatus{}, fmt.Errorf("daemon probe at %s: %w", address, ctxErr)
		}
		var urlErr *url.Error
		if bounded.Err() != nil && errors.As(err, &urlErr) {
			urlErr.Err = probeTimeoutText(urlErr.Err, " (Client.Timeout exceeded while awaiting headers)")
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
	body := probeBody{reader: response.Body, bound: bounded}
	if err := json.NewDecoder(body).Decode(&status); err != nil {
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

// probeTimeoutError is the error net/http's Client.Timeout returns once its
// deadline passed, hand-written because the probe's deadline is now a context
// on the clock seam: the same text (the failure's own plus net/http's
// suffix), a net.Error that timed out, and context.DeadlineExceeded.
type probeTimeoutError struct{ text string }

func (err probeTimeoutError) Error() string    { return err.text }
func (probeTimeoutError) Timeout() bool        { return true }
func (probeTimeoutError) Temporary() bool      { return true }
func (probeTimeoutError) Is(target error) bool { return target == context.DeadlineExceeded }

// probeTimeoutText is the error a Client.Timeout past its deadline gives for
// err: err's text with net/http's suffix for the phase it failed in.
func probeTimeoutText(err error, suffix string) error {
	return probeTimeoutError{text: err.Error() + suffix}
}

// probeBody reads the status document as net/http's Client.Timeout body did:
// a read failing once the probe's deadline passed carries its suffix.
type probeBody struct {
	reader io.Reader
	bound  context.Context
}

func (body probeBody) Read(p []byte) (int, error) {
	n, err := body.reader.Read(p)
	if err != nil && err != io.EOF && body.bound.Err() != nil {
		err = probeTimeoutText(err, " (Client.Timeout or context cancellation while reading body)")
	}
	return n, err
}

// DaemonReachability probes the configured loopback daemon for doctor. The
// probe's own error travels out whole, so the report says WHICH failure it
// was — a refused connection, a foreign service, a body that did not parse —
// instead of one "unreachable" for every one of them.
func DaemonReachability(runtime config.Runtime) (DaemonStatus, error) {
	return ProbeDaemon("127.0.0.1:" + strconv.Itoa(runtime.Config.MCP.HTTP.Port))
}
