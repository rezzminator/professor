package mcpserv

import (
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// ProxyWireVersion is the version shared by a per-chat stdio proxy and the
// daemon that accepts its explicit caller identity.
const ProxyWireVersion = 1

// ProxyIdentity is the caller seat a per-chat proxy places in _meta.pfmProxy.
// It is an identity assertion, not authorization: the daemon listens only on
// loopback, and the accepted threat model permits another local process to
// state the same payload. The boundary protects against a confused chat, not
// a malicious local process.
type ProxyIdentity struct {
	Version    int    `json:"v"`
	Session    string `json:"session"`
	SocketPath string `json:"socketPath"`
	SocketName string `json:"socketName"`
	Pane       string `json:"pane"`
	Engine     string `json:"engine"`
	ID         string `json:"id"`
	Source     string `json:"source"`
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
	if strings.TrimSpace(payload.Session) == "" ||
		strings.TrimSpace(payload.Session) != payload.Session || containsControl(payload.Session) {
		return ProxyIdentity{},
			"MCP _meta.pfmProxy must carry a non-empty session without whitespace or control characters",
			nil
	}
	return payload, "", nil
}

func (payload ProxyIdentity) callerIdentity() resolve.Identity {
	return resolve.Identity{
		Session:    payload.Session,
		SocketPath: payload.SocketPath,
		SocketName: payload.SocketName,
		Pane:       payload.Pane,
		Engine:     payload.Engine,
		ID:         payload.ID,
		Source:     "mcp-proxy",
	}
}
