package mcpserv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

func TestParseProxyIdentityAcceptsWirePayload(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestBirthProxyErrorPayload(t *testing.T) {
	for _, kind := range []string{"not_in_chat", "not_registered", "ambiguous", "failed"} {
		_, detail, err := parseProxyIdentity(
			map[string]any{"v": 1, "error": map[string]any{"kind": kind, "message": "lookup failed"}},
		)
		if err != nil || detail != "" {
			t.Errorf("kind %s: detail=%q err=%v", kind, detail, err)
		}
	}
	_, detail, err := parseProxyIdentity(map[string]any{"v": 1, "error": map[string]any{"kind": "x", "message": "bad"}})
	if err != nil || detail != `MCP _meta.pfmProxy error kind "x" is unknown` {
		t.Fatalf("unknown kind: detail=%q err=%v", detail, err)
	}
}

func TestBirthProxyForwardsCurrentHerdrSeatAndErrors(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{}}`)
	}))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.sidDir = t.TempDir()
	if err := os.WriteFile(
		filepath.Join(proxy.sidDir, "herdr.sock.wC:p24"),
		[]byte("/transcripts/conversation-a.jsonl\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	identity := resolve.Identity{
		Backend:        "herdr",
		SeatID:         "87caf6bf",
		Session:        "87caf6bf",
		HerdrWorkspace: "wC",
		SocketPath:     "/h/herdr.sock",
		SocketName:     "herdr.sock",
		Pane:           "wC:p24",
		Engine:         "claude",
		ID:             "conversation-a",
		Source:         "herdr-ancestry",
	}
	currentErr := resolve.ErrNotRegistered
	proxy.identify = func(context.Context) (resolve.Identity, error) { return identity, currentErr }
	var output bytes.Buffer
	for _, kind := range []string{"not_registered", "ok", "ambiguous", "failed", "not_in_chat"} {
		switch kind {
		case "ok":
			currentErr = nil
		case "ambiguous":
			currentErr = resolve.ErrAmbiguous
		case "failed":
			currentErr = fmt.Errorf("herdr timeout")
		case "not_in_chat":
			currentErr = resolve.ErrNotInChat
		}
		proxy.forward(context.Background(), []byte(proxyTestToolCall(2, "whoami", `{}`)), &output)
		params := received["params"].(map[string]any)
		meta := params["_meta"].(map[string]any)
		got := meta["pfmProxy"].(map[string]any)
		if kind == "ok" {
			if got["session"] != "87caf6bf" || got["backend"] != "herdr" || got["seatId"] != "87caf6bf" ||
				got["herdrWorkspace"] != "wC" ||
				got["source"] != "herdr-ancestry" {
				t.Fatalf("current Herdr identity=%v", got)
			}
		} else {
			failure, _ := got["error"].(map[string]any)
			if failure["kind"] != kind {
				t.Errorf("kind %s: forwarded=%v", kind, got)
			}
			if kind == "not_registered" && (got["backend"] != "herdr" || got["pane"] != "wC:p24") {
				t.Errorf("unregistered lost location: %v", got)
			}
			if kind != "not_registered" && len(got) != 2 {
				t.Errorf("unresolved kind %s retained identity: %v", kind, got)
			}
		}
	}
}
