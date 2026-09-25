package mcpserv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const proxyRecoveryUninitialized = `{"jsonrpc":"2.0","id":2,"error":{"code":0,"message":"method \"tools/call\" is invalid during session initialization"}}`

// proxyRecoveryDaemon fakes the daemon's HTTP door: initialize opens session
// "s1", and each tools/call is answered by answer with the session header it carried.
type proxyRecoveryDaemon struct {
	mutex       sync.Mutex
	posts       []string
	initializes atomic.Int32
	answer      func(frame proxyFrame, sessionID string) *http.Response
}

func proxyRecoveryJSON(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// proxyTestSessionLost is the daemon's answer to a session it no longer holds.
func proxyTestSessionLost() *http.Response {
	return &http.Response{
		StatusCode: http.StatusNotFound, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader("session not found\n")),
	}
}

func (daemon *proxyRecoveryDaemon) proxy() *stdioProxy {
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.identity = nil
	proxy.endpoint = "http://proxy.test/mcp/chat"
	proxy.retryDelay = time.Millisecond
	proxy.client = &http.Client{Transport: proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			return nil, fmt.Errorf("decode proxy recovery request: %w", err)
		}
		sessionID := request.Header.Get("Mcp-Session-Id")
		daemon.mutex.Lock()
		daemon.posts = append(daemon.posts, frame.Method+"@"+sessionID)
		daemon.mutex.Unlock()
		switch frame.Method {
		case proxyInitializeMethod:
			daemon.initializes.Add(1)
			response := proxyRecoveryJSON(
				http.StatusOK,
				`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`,
			)
			response.Header.Set("Mcp-Session-Id", "s1")
			return response, nil
		case "notifications/initialized":
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: http.NoBody}, nil
		default:
			return daemon.answer(frame, sessionID), nil
		}
	})}
	return proxy
}

func (daemon *proxyRecoveryDaemon) recorded() []string {
	daemon.mutex.Lock()
	defer daemon.mutex.Unlock()
	return append([]string(nil), daemon.posts...)
}

func proxyRecoveryError(t *testing.T, frame map[string]any) (float64, string) {
	t.Helper()
	errorObject, ok := frame["error"].(map[string]any)
	if !ok {
		t.Fatalf("response %+v is not a JSON-RPC error", frame)
	}
	code, _ := errorObject["code"].(float64)
	message, _ := errorObject["message"].(string)
	return code, message
}

// A: Claude Code probes server/discover before initialize. The probe must be
// answered locally and never cost the session, even after the retry window.
func TestStdioProxyDiscoverProbeKeepsSessionPastRetryWindow(t *testing.T) {
	var calls [][]string
	daemon := proxyTestDaemon(proxyTestService("daemon", nil, &calls))
	var discoverPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path != "/status" {
			content, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			}
			if strings.Contains(string(content), "server/discover") {
				discoverPosts.Add(1)
			}
			request.Body = io.NopCloser(strings.NewReader(string(content)))
		}
		daemon.ServeHTTP(writer, request)
	}))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.identity = nil
	proxy.retryWindow = 150 * time.Millisecond
	proxy.retryDelay = 5 * time.Millisecond
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, `{"jsonrpc":"2.0","id":"server-discover-probe-1","method":"server/discover","params":{}}`)
	if code, message := proxyRecoveryError(t, harness.read(t)); code != -32601 {
		t.Errorf("discover probe answered %v %q, want -32601", code, message)
	}
	harness.write(t, proxyTestInitialize)
	if _, failed := harness.read(t)["error"]; failed {
		t.Fatal("initialize after the discover probe failed")
	}
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	harness.write(t, proxyTestToolCall(2, "chat_new", `{"name":"inside-window"}`))
	if got := proxyTestStructured(t, harness.read(t))["message"]; got != "daemon" {
		t.Fatalf("call inside the retry window = %v, want daemon", got)
	}
	<-time.After(3 * proxy.retryWindow)
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"past-window"}`))
	if got := proxyTestStructured(t, harness.read(t))["message"]; got != "daemon" {
		t.Fatalf("call past the retry window = %v, want daemon", got)
	}
	if posts := discoverPosts.Load(); posts != 0 {
		t.Errorf("server/discover reached the daemon %d times, want answered locally", posts)
	}
}

// B: a daemon 4xx that is not a lost session is the request's answer, at once;
// the session survives it.
func TestStdioProxyReturnsDaemonRejectionAndKeepsSession(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       float64
		parts      []string
	}{
		{"method not handled", `JSON RPC not handled: "tools/other" unsupported`, -32601, []string{"not handled"}},
		{"other rejection", "request body is malformed", -32603, []string{"rejected", "HTTP 400", "malformed"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon := &proxyRecoveryDaemon{answer: func(frame proxyFrame, _ string) *http.Response {
				if string(frame.ID) == "2" {
					return &http.Response{
						StatusCode: http.StatusBadRequest, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(test.body + "\n")),
					}
				}
				return proxyRecoveryJSON(http.StatusOK, `{"jsonrpc":"2.0","id":3,"result":{}}`)
			}}
			proxy := daemon.proxy()
			harness := startProxyTestHarness(t, proxy)
			harness.write(t, proxyTestInitialize)
			_ = harness.read(t)
			harness.write(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
			started := time.Now()
			harness.write(t, `{"jsonrpc":"2.0","id":2,"method":"tools/other","params":{}}`)
			code, message := proxyRecoveryError(t, harness.read(t))
			if code != test.code {
				t.Fatalf("rejection code = %v (%q), want %v", code, message, test.code)
			}
			for _, part := range test.parts {
				if !strings.Contains(message, part) {
					t.Errorf("rejection %q does not name %q", message, part)
				}
			}
			if strings.Contains(message, "unreachable") {
				t.Errorf("rejection %q claims the daemon was unreachable", message)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("rejection took %v, want an immediate answer", elapsed)
			}
			harness.write(t, proxyTestToolCall(3, "chat_status", `{"target":"seat"}`))
			if _, failed := harness.read(t)["error"]; failed {
				t.Fatal("call after a rejection failed")
			}
			want := []string{"initialize@", "notifications/initialized@s1", "tools/other@s1", "tools/call@s1"}
			if got := daemon.recorded(); !reflect.DeepEqual(got, want) {
				t.Fatalf("daemon posts = %q, want %q", got, want)
			}
		})
	}
}

// C: no non-initialize frame leaves without a session while a handshake is stored.
func TestStdioProxyReplaysHandshakeBeforeSessionlessCall(t *testing.T) {
	daemon := &proxyRecoveryDaemon{answer: func(_ proxyFrame, sessionID string) *http.Response {
		if sessionID == "" {
			return proxyRecoveryJSON(http.StatusOK, proxyRecoveryUninitialized)
		}
		return proxyRecoveryJSON(http.StatusOK, `{"jsonrpc":"2.0","id":2,"result":{}}`)
	}}
	proxy := daemon.proxy()
	proxy.storeHandshake(proxyInitializeMethod, []byte(proxyTestInitialize))
	proxy.storeHandshake("notifications/initialized", []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	response, err := proxy.sendWithRetry(
		context.Background(), []byte(proxyTestToolCall(2, "chat_status", `{"target":"seat"}`)), false,
	)
	if err != nil {
		t.Fatalf("session-less call: %v", err)
	}
	if strings.Contains(string(response), "error") {
		t.Fatalf("session-less call answered %s", response)
	}
	want := []string{"initialize@", "notifications/initialized@s1", "tools/call@s1"}
	if got := daemon.recorded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("daemon posts = %q, want %q", got, want)
	}
}

// D: a 200 "invalid during session initialization" means the session is lost:
// re-initialize and replay once; a second one goes back to the client as-is.
func TestStdioProxyReinitializesOnceOnUninitializedSession(t *testing.T) {
	for _, test := range []struct {
		name      string
		recovers  bool
		wantPosts []string
	}{
		{"recovers", true, []string{"tools/call@stale", "initialize@", "notifications/initialized@s1", "tools/call@s1"}},
		{"second occurrence returns", false, []string{
			"tools/call@stale", "initialize@", "notifications/initialized@s1", "tools/call@s1",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon := &proxyRecoveryDaemon{answer: func(_ proxyFrame, sessionID string) *http.Response {
				if sessionID == "s1" && test.recovers {
					return proxyRecoveryJSON(http.StatusOK, `{"jsonrpc":"2.0","id":2,"result":{}}`)
				}
				return proxyRecoveryJSON(http.StatusOK, proxyRecoveryUninitialized)
			}}
			proxy := daemon.proxy()
			proxy.sessionID = "stale"
			proxy.protocol = "2025-06-18"
			proxy.storeHandshake(proxyInitializeMethod, []byte(proxyTestInitialize))
			proxy.storeHandshake(
				"notifications/initialized",
				[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
			)
			response, err := proxy.sendWithRetry(
				context.Background(), []byte(proxyTestToolCall(2, "chat_status", `{"target":"seat"}`)), false,
			)
			if err != nil {
				t.Fatalf("uninitialized-session call: %v", err)
			}
			if lost := strings.Contains(string(response), proxyUninitializedError); lost == test.recovers {
				t.Fatalf("response %s after backstop, recovers=%v", response, test.recovers)
			}
			if got := daemon.recorded(); !reflect.DeepEqual(got, test.wantPosts) {
				t.Fatalf("daemon posts = %q, want %q", got, test.wantPosts)
			}
		})
	}
}
