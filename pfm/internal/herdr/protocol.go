package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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
		Type     string  `json:"type"`
		Version  string  `json:"version"`
		Protocol *uint32 `json:"protocol"`
	}
	if err := json.Unmarshal(result, &pong); err != nil {
		return ProtocolStatus{}, fmt.Errorf("malformed ping result: herdr socket %s: %w", socket, err)
	}
	if pong.Type != "pong" || strings.TrimSpace(pong.Version) == "" || pong.Protocol == nil {
		return ProtocolStatus{}, fmt.Errorf(
			"malformed ping result: herdr socket %s: type=pong, version and uint32 protocol are required",
			socket,
		)
	}
	status := ProtocolStatus{Version: pong.Version, Protocol: *pong.Protocol}
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
