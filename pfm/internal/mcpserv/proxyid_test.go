package mcpserv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
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
	}
	want := ProxyIdentity{
		Version:    ProxyWireVersion,
		Session:    "cc-seat",
		SocketPath: "/tmp/tmux-1000/cc-seat",
		SocketName: "cc-seat",
		Pane:       "%7",
		Engine:     "claude",
		ID:         "session-id",
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
	const unread = "caller identity lookup failed without a message"
	for _, test := range []struct {
		kind, message      any
		wantKind, wantText string
	}{
		{"not_in_chat", "lookup failed", "not_in_chat", "lookup failed"},
		{"not_registered", "lookup failed", "not_registered", "lookup failed"},
		{"ambiguous", "lookup failed", "ambiguous", "lookup failed"},
		{"failed", "lookup failed", "failed", "lookup failed"},
		{"not_registered", " ", "not_registered", unread},
		{"x", "bad", "failed", `bad (unknown error kind "x")`},
		{float64(3), nil, "failed", unread + " (unknown error kind 3)"},
	} {
		raw := map[string]any{"kind": test.kind, "message": test.message}
		got, detail, err := parseProxyIdentity(map[string]any{"v": 1, "error": raw})
		want := ProxyIdentityError{Kind: test.wantKind, Message: test.wantText}
		if err != nil || detail != "" || got.Error == nil || *got.Error != want {
			t.Errorf(
				"error payload %v: parsed error %+v, detail=%q err=%v; want %+v",
				raw,
				got.Error,
				detail,
				err,
				want,
			)
		}
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
				got["herdrWorkspace"] != "wC" {
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

// A request whose caller is not proven is never placed as "outside Herdr":
// withBirthCaller carries it unproven, and with a Herdr server running every
// caller's chat is hosted in Herdr: a Herdr seat's in its pane, any other's on
// the running server.
func TestWithBirthCaller(t *testing.T) {
	herdrSeat := resolve.Identity{Backend: "herdr", SocketPath: "/h/herdr.sock", Pane: "wC:p24", HerdrWorkspace: "wC"}
	onServer := spawn.BackendChoice{Backend: "herdr", HerdrSocket: "/env/herdr.sock"}
	const unproven = "caller unproven: "
	for _, tc := range []struct {
		name      string
		ambient   bool
		caller    callerIdentity
		want      spawn.BackendChoice
		callerErr string
	}{
		{name: "no caller identity", want: onServer, callerErr: unproven + "the MCP request carried no caller identity"},
		{
			name: "a client in no chat pane", caller: callerIdentity{present: true, absent: true, err: errors.New("not in chat")},
			want: onServer, callerErr: unproven + "the MCP client's process is in no pfm chat pane (not in chat)",
		},
		{
			name:   "a caller that did not resolve",
			caller: callerIdentity{present: true, detail: "herdr timed out", err: errors.New("herdr timed out")},
			want:   onServer, callerErr: unproven + "the MCP caller did not resolve: herdr timed out",
		},
		{
			name: "a Herdr seat", caller: callerIdentity{present: true, valid: true, identity: herdrSeat},
			want: spawn.BackendChoice{Backend: "herdr", HerdrSocket: "/h/herdr.sock", HerdrPane: "wC:p24", HerdrWorkspace: "wC"},
		},
		{
			name: "a tmux seat", want: onServer,
			caller: callerIdentity{present: true, valid: true, identity: resolve.Identity{Backend: "tmux"}},
		},
		{name: "a stdio server's own plain process", ambient: true, want: onServer, callerErr: resolve.ErrNotInChat.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{
				"TMUX", "TMUX_PANE", "HERDR_SOCKET_PATH", "HERDR_PANE_ID",
				"CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID",
			} {
				t.Setenv(name, "")
			}
			ctx, recorder := obs.Test(t)
			ctx = chat.WithHerdrServer(ctx, spawn.HerdrServer{
				Socket: "/env/herdr.sock", Up: func(context.Context, string) error { return nil },
			})
			service := newService("test", &backend{allowAmbientIdentity: tc.ambient})
			choice, err := chat.BirthBackend(service.withBirthCaller(ctx, "mcp.open", tc.caller), "cli.open", "auto")
			choice.Places = nil
			if err != nil || !reflect.DeepEqual(choice, tc.want) {
				t.Fatalf("choice=%+v error=%v; want %+v", choice, err, tc.want)
			}
			records := recorder.Records()
			if callerErr, _ := records[len(records)-1].Field("caller_err"); callerErr != tc.callerErr {
				t.Fatalf("caller_err=%v, want %q", callerErr, tc.callerErr)
			}
		})
	}
}
