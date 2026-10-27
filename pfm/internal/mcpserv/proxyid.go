package mcpserv

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// ProxyWireVersion is the version shared by a per-chat stdio proxy and the
// daemon that accepts its explicit caller identity.
const ProxyWireVersion = 1

const (
	proxyNotInChat = "not_in_chat"
	proxyFailed    = "failed"
)

// ProxyIdentity is the caller seat a per-chat proxy places in _meta.pfmProxy.
// It is an identity assertion, not authorization: the daemon listens only on
// loopback, and the accepted threat model permits another local process to
// state the same payload. The boundary protects against a confused chat, not
// a malicious local process.
type ProxyIdentity struct {
	Version        int                 `json:"v"`
	Session        string              `json:"session,omitempty"`
	Backend        string              `json:"backend,omitempty"`
	SeatID         string              `json:"seatId,omitempty"`
	HerdrWorkspace string              `json:"herdrWorkspace,omitempty"`
	Error          *ProxyIdentityError `json:"error,omitempty"`
	SocketPath     string              `json:"socketPath,omitempty"`
	SocketName     string              `json:"socketName,omitempty"`
	Pane           string              `json:"pane,omitempty"`
	Engine         string              `json:"engine,omitempty"`
	ID             string              `json:"id,omitempty"`
	Source         string              `json:"source,omitempty"`
}

// parseProxyIdentity decodes the untyped JSON object carried in
// _meta.pfmProxy. A malformed identity is a present-but-invalid answer; an
// unsupported version is an error so an old proxy can never look like an
// anonymous caller.
func parseProxyIdentity(raw any) (ProxyIdentity, string, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return ProxyIdentity{}, "MCP _meta.pfmProxy must be an object", nil
	}

	rawVersion, versionPresent := object["v"]
	received := "missing"
	version := 0
	versionValid := false
	if versionPresent {
		received = fmt.Sprint(rawVersion)
		switch typed := rawVersion.(type) {
		case int:
			version, versionValid = typed, true
		case float64:
			converted := int(typed)
			if float64(converted) == typed {
				version, versionValid = converted, true
			}
		}
	}
	if !versionValid || version != ProxyWireVersion {
		return ProxyIdentity{}, "", fmt.Errorf(
			"proxy wire version %s is unsupported; daemon requires version %d; "+
				"restart the chat so its engine launches the new proxy",
			received,
			ProxyWireVersion,
		)
	}

	payload := ProxyIdentity{Version: version}
	fields := []struct {
		name  string
		value *string
	}{
		{name: "session", value: &payload.Session},
		{name: "backend", value: &payload.Backend},
		{name: "seatId", value: &payload.SeatID},
		{name: "herdrWorkspace", value: &payload.HerdrWorkspace},
		{name: "socketPath", value: &payload.SocketPath},
		{name: "socketName", value: &payload.SocketName},
		{name: "pane", value: &payload.Pane},
		{name: "engine", value: &payload.Engine},
		{name: "id", value: &payload.ID},
		{name: "source", value: &payload.Source},
	}
	for _, field := range fields {
		value, exists := object[field.name]
		if !exists {
			continue
		}
		text, isString := value.(string)
		if !isString {
			return ProxyIdentity{}, "MCP _meta.pfmProxy " + field.name + " must be a string", nil
		}
		*field.value = text
	}
	if rawError, exists := object["error"]; exists {
		errorObject, ok := rawError.(map[string]any)
		if !ok {
			return ProxyIdentity{}, "MCP _meta.pfmProxy error must be an object", nil
		}
		kind, kindOK := errorObject["kind"].(string)
		message, messageOK := errorObject["message"].(string)
		if !kindOK || !messageOK || strings.TrimSpace(message) == "" {
			return ProxyIdentity{}, "MCP _meta.pfmProxy error must carry string kind and non-empty message", nil
		}
		switch kind {
		case proxyNotInChat, statusNotRegistered, statusAmbiguous, proxyFailed:
		default:
			return ProxyIdentity{}, fmt.Sprintf("MCP _meta.pfmProxy error kind %q is unknown", kind), nil
		}
		payload.Error = &ProxyIdentityError{Kind: kind, Message: message}
	}
	if payload.Backend != "" && payload.Backend != "tmux" && payload.Backend != "herdr" {
		return ProxyIdentity{}, fmt.Sprintf("MCP _meta.pfmProxy backend %q is unknown", payload.Backend), nil
	}
	if (payload.Session == "" && payload.Error == nil) ||
		strings.TrimSpace(payload.Session) != payload.Session || containsControl(payload.Session) {
		return ProxyIdentity{},
			"MCP _meta.pfmProxy must carry a non-empty session without whitespace or control characters",
			nil
	}
	return payload, "", nil
}

// ProxyIdentityError carries a failed lookup without making the caller anonymous.
type ProxyIdentityError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func proxyErrorKind(err error) string {
	switch {
	case errors.Is(err, resolve.ErrNotInChat):
		return proxyNotInChat
	case errors.Is(err, resolve.ErrNotRegistered):
		return statusNotRegistered
	case errors.Is(err, resolve.ErrAmbiguous):
		return statusAmbiguous
	default:
		return proxyFailed
	}
}

func (failure ProxyIdentityError) Error() string { return failure.Message }

func (failure ProxyIdentityError) Unwrap() error {
	switch failure.Kind {
	case proxyNotInChat:
		return resolve.ErrNotInChat
	case statusNotRegistered:
		return resolve.ErrNotRegistered
	case statusAmbiguous:
		return resolve.ErrAmbiguous
	default:
		return nil
	}
}
