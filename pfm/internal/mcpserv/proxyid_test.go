package mcpserv

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseProxyIdentityAcceptsWirePayload(t *testing.T) {
	raw := map[string]any{
		"v":          float64(ProxyWireVersion),
		"session":    "cc-seat",
		"socketPath": "/tmp/tmux-1000/cc-seat",
		"socketName": "cc-seat",
		"pane":       "%7",
		"engine":     "claude",
		"id":         "session-id",
		"source":     "tmux",
	}
	want := ProxyIdentity{
		Version:    ProxyWireVersion,
		Session:    "cc-seat",
		SocketPath: "/tmp/tmux-1000/cc-seat",
		SocketName: "cc-seat",
		Pane:       "%7",
		Engine:     "claude",
		ID:         "session-id",
		Source:     "tmux",
	}
	got, detail, err := parseProxyIdentity(raw)
	if err != nil || detail != "" || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseProxyIdentity() = %+v, detail=%q, err=%v; want %+v", got, detail, err, want)
	}
}

func TestParseProxyIdentityNamesMalformedPayloads(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{"v": ProxyWireVersion, "session": "cc-seat"}
	}
	tests := []struct {
		name   string
		raw    any
		detail string
	}{
		{name: "not object", raw: []any{"cc-seat"}, detail: "must be an object"},
		{
			name:   "session not string",
			raw:    map[string]any{"v": ProxyWireVersion, "session": 7},
			detail: "session must be a string",
		},
		{name: "session absent", raw: map[string]any{"v": ProxyWireVersion}, detail: "non-empty session"},
		{name: "session empty", raw: map[string]any{"v": ProxyWireVersion, "session": ""}, detail: "non-empty session"},
		{
			name:   "session whitespace",
			raw:    map[string]any{"v": ProxyWireVersion, "session": " cc-seat "},
			detail: "without whitespace",
		},
		{
			name:   "session control",
			raw:    map[string]any{"v": ProxyWireVersion, "session": "cc-seat\n"},
			detail: "control characters",
		},
		{name: "seat field not string", raw: func() map[string]any {
			payload := valid()
			payload["pane"] = 7
			return payload
		}(), detail: "pane must be a string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, detail, err := parseProxyIdentity(test.raw)
			if err != nil || !strings.Contains(detail, test.detail) {
				t.Fatalf("detail=%q err=%v, want nil error and detail containing %q", detail, err, test.detail)
			}
		})
	}
}

func TestParseProxyIdentityRejectsUnsupportedWireVersions(t *testing.T) {
	tests := []struct {
		name     string
		version  any
		received string
		include  bool
	}{
		{name: "missing", received: "missing"},
		{name: "zero integer", version: 0, received: "0", include: true},
		{name: "zero JSON number", version: float64(0), received: "0", include: true},
		{name: "next", version: float64(ProxyWireVersion + 1), received: "2", include: true},
		{name: "not numeric", version: "one", received: "one", include: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := map[string]any{"session": "cc-seat"}
			if test.include {
				payload["v"] = test.version
			}
			_, detail, err := parseProxyIdentity(payload)
			if err == nil || detail != "" {
				t.Fatalf("detail=%q err=%v, want a version error", detail, err)
			}
			message := err.Error()
			for _, part := range []string{
				test.received,
				"requires version 1",
				"restart the chat",
			} {
				if !strings.Contains(message, part) {
					t.Errorf("error %q does not name %q", message, part)
				}
			}
		})
	}
}
