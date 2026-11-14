package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// PaneGone reports whether err is Herdr refusing a pane id it does not know
// (pane_not_found, or not_found from an older Herdr).
func PaneGone(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && (refusal.Code == paneNotFound || refusal.Code == "not_found")
}

// SocketFileGone is the narrower SocketGone: a dial that found no socket file
// at its path (ENOENT), so no Herdr server can listen there any more. A
// refused connection, a timeout or any answer may be a server that still runs.
func SocketFileGone(err error) bool {
	var notSent *InputNotSentError
	var dial *net.OpError
	return errors.As(err, &notSent) && errors.As(err, &dial) && dial.Op == "dial" && errors.Is(err, syscall.ENOENT)
}

// ServerStopping reports whether err is Herdr refusing a request because its
// server is shutting down (server_unavailable): its panes are about to go.
func ServerStopping(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == "server_unavailable"
}

// protocolVersionUnknown is the Version of a ping that names no release.
const protocolVersionUnknown = "unknown"

// ProtocolStatus is the measured ping identity of one server.
type ProtocolStatus struct {
	Version  string
	Protocol uint32
}

// ProtocolMismatch reports a reachable server with an unsupported protocol.
type ProtocolMismatch struct {
	Protocol uint32
}

func (m *ProtocolMismatch) Error() string {
	return fmt.Sprintf(
		"herdr protocol %d DIVERGES from supported %d — herdr seats disabled",
		m.Protocol,
		SupportedProtocol,
	)
}

// ProbeProtocol validates a ping identity and checks its protocol independently
// of the release version. On mismatch it preserves the measured identity.
func ProbeProtocol(ctx context.Context, socket string) (ProtocolStatus, error) {
	result, err := SeatRequest(ctx, socket, "ping", struct{}{})
	if err != nil {
		return ProtocolStatus{}, err
	}
	var pong struct {
		Type     string          `json:"type"`
		Version  json.RawMessage `json:"version"`
		Protocol *uint32         `json:"protocol"`
	}
	if err := json.Unmarshal(result, &pong); err != nil {
		return ProtocolStatus{}, fmt.Errorf("malformed ping result: herdr socket %s: %w", socket, err)
	}
	if pong.Type != "pong" || pong.Protocol == nil {
		return ProtocolStatus{}, fmt.Errorf(
			"malformed ping result: herdr socket %s: type=pong and uint32 protocol are required",
			socket,
		)
	}
	// The version is only shown (doctor); the protocol alone decides, so a
	// version that is absent, empty or not a string reads as unknown.
	version := protocolVersionUnknown
	var shown string
	if json.Unmarshal(pong.Version, &shown) == nil && strings.TrimSpace(shown) != "" {
		version = shown
	}
	status := ProtocolStatus{Version: version, Protocol: *pong.Protocol}
	if status.Protocol != SupportedProtocol {
		return status, &ProtocolMismatch{Protocol: status.Protocol}
	}
	return status, nil
}

// CheckProtocol refuses native-seat birth on an unreachable, malformed or
// incompatible endpoint. It has no effect on existing tmux-backed seats.
func CheckProtocol(ctx context.Context, socket string) error {
	_, err := ProbeProtocol(ctx, socket)
	return err
}
