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

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

func TestStdioProxyForwardsCallsThroughDaemon(t *testing.T) {
	t.Setenv(resolve.CodexThreadEnv, "ambient-thread-without-daemon-row")
	var calls [][]string
	service := proxyTestService("daemon", nil, &calls)
	server := httptest.NewServer(proxyTestDaemon(service))
	defer server.Close()

	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.identity = nil
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestInitialize)
	_ = harness.read(t)
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	harness.write(t, proxyTestToolCall(2, "chat_new", `{"name":"child"}`))
	structured := proxyTestStructured(t, harness.read(t))
	if structured["message"] != "daemon" {
		t.Fatalf("forwarded chat_new = %+v, want daemon marker", structured)
	}
}

func TestStdioProxyLateFailurePreservesRecoveredSession(t *testing.T) {
	const oldSession = "session-old"
	const recoveredSession = "session-recovered"
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	toolHeaders := make(chan string, 4)
	var recoveryToolCalls atomic.Int32
	transport := proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		content, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, fmt.Errorf("read proxy test request: %w", err)
		}
		var frame proxyFrame
		if err := json.Unmarshal(content, &frame); err != nil {
			return nil, fmt.Errorf("decode proxy test request: %w", err)
		}
		sessionID := request.Header.Get("Mcp-Session-Id")
		switch frame.Method {
		case proxyInitializeMethod:
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`,
				)),
			}
			response.Header.Set("Content-Type", "application/json")
			response.Header.Set("Mcp-Session-Id", recoveredSession)
			return response, nil
		case "notifications/initialized":
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: http.NoBody}, nil
		case "tools/call":
			toolHeaders <- sessionID
			switch string(frame.ID) {
			case "2":
				close(oldStarted)
				<-releaseOld
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: http.NoBody,
				}, nil
			case "3":
				if recoveryToolCalls.Add(1) == 1 {
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: http.NoBody,
					}, nil
				}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":3,"result":{}}`)),
			}, nil
		default:
			return nil, fmt.Errorf("unexpected proxy test method %q", frame.Method)
		}
	})
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.endpoint = "http://proxy.test/mcp/chat"
	proxy.client = &http.Client{Transport: transport}
	proxy.retryWindow = 2 * time.Second
	proxy.retryDelay = time.Millisecond
	proxy.sessionID = oldSession
	proxy.protocol = "2025-06-18"
	proxy.storeHandshake(proxyInitializeMethod, []byte(proxyTestInitialize))
	proxy.storeHandshake("notifications/initialized", []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))

	oldContext, cancelOld := context.WithCancel(context.Background())
	oldDone := make(chan error, 1)
	go func() {
		_, err := proxy.sendWithRetry(
			oldContext,
			[]byte(proxyTestToolCall(2, "chat_status", `{"target":"old"}`)),
			false,
		)
		oldDone <- err
	}()
	<-oldStarted
	if _, err := proxy.sendWithRetry(
		context.Background(),
		[]byte(proxyTestToolCall(3, "chat_status", `{"target":"recovery"}`)),
		false,
	); err != nil {
		t.Fatalf("recover current session: %v", err)
	}
	cancelOld()
	close(releaseOld)
	if err := <-oldDone; err == nil {
		t.Fatal("late old-generation request unexpectedly succeeded")
	}
	if _, err := proxy.post(
		context.Background(),
		[]byte(proxyTestToolCall(4, "chat_status", `{"target":"after"}`)),
	); err != nil {
		t.Fatalf("request after late failure: %v", err)
	}
	gotHeaders := []string{<-toolHeaders, <-toolHeaders, <-toolHeaders, <-toolHeaders}
	wantHeaders := []string{oldSession, oldSession, recoveredSession, recoveredSession}
	if !reflect.DeepEqual(gotHeaders, wantHeaders) {
		t.Fatalf("tool session headers = %q, want %q", gotHeaders, wantHeaders)
	}
}

func TestStdioProxyCurrentFailureClearsSessionBeforeRecovery(t *testing.T) {
	const oldSession = "session-old"
	const recoveredSession = "session-recovered"
	var toolCalls atomic.Int32
	var headers []string
	var mutex sync.Mutex
	transport := proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		content, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, fmt.Errorf("read proxy test request: %w", err)
		}
		var frame proxyFrame
		if err := json.Unmarshal(content, &frame); err != nil {
			return nil, fmt.Errorf("decode proxy test request: %w", err)
		}
		mutex.Lock()
		headers = append(headers, request.Header.Get("Mcp-Session-Id"))
		mutex.Unlock()
		switch frame.Method {
		case proxyInitializeMethod:
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`,
				)),
			}
			response.Header.Set("Mcp-Session-Id", recoveredSession)
			return response, nil
		case "notifications/initialized":
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: http.NoBody}, nil
		case "tools/call":
			if toolCalls.Add(1) == 1 {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: http.NoBody,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":2,"result":{}}`)),
			}, nil
		default:
			return nil, fmt.Errorf("unexpected proxy test method %q", frame.Method)
		}
	})
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.endpoint = "http://proxy.test/mcp/chat"
	proxy.client = &http.Client{Transport: transport}
	proxy.retryDelay = time.Millisecond
	proxy.sessionID = oldSession
	proxy.protocol = "2025-06-18"
	proxy.storeHandshake(proxyInitializeMethod, []byte(proxyTestInitialize))
	proxy.storeHandshake("notifications/initialized", []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if _, err := proxy.sendWithRetry(
		context.Background(),
		[]byte(proxyTestToolCall(2, "chat_status", `{"target":"seat"}`)),
		false,
	); err != nil {
		t.Fatalf("recover current generation: %v", err)
	}
	mutex.Lock()
	gotHeaders := append([]string(nil), headers...)
	mutex.Unlock()
	wantHeaders := []string{oldSession, "", recoveredSession, recoveredSession}
	if !reflect.DeepEqual(gotHeaders, wantHeaders) {
		t.Fatalf("recovery session headers = %q, want %q", gotHeaders, wantHeaders)
	}
}

func TestStdioProxyStaleSuccessCannotReplaceNewSession(t *testing.T) {
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	transport := proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		content, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, fmt.Errorf("read proxy test request: %w", err)
		}
		var frame proxyFrame
		if err := json.Unmarshal(content, &frame); err != nil {
			return nil, fmt.Errorf("decode proxy test request: %w", err)
		}
		sessionID := "session-new"
		protocol := "protocol-new"
		if string(frame.ID) == "2" {
			close(oldStarted)
			<-releaseOld
			sessionID = "session-stale"
			protocol = "protocol-stale"
		}
		response := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"` + protocol + `"}}`,
			)),
		}
		response.Header.Set("Mcp-Session-Id", sessionID)
		return response, nil
	})
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.endpoint = "http://proxy.test/mcp/chat"
	proxy.client = &http.Client{Transport: transport}
	proxy.sessionID = "session-old"
	proxy.protocol = "protocol-old"
	oldDone := make(chan error, 1)
	go func() {
		_, err := proxy.post(
			context.Background(),
			[]byte(proxyTestToolCall(2, "chat_status", `{"target":"old"}`)),
		)
		oldDone <- err
	}()
	<-oldStarted
	if _, err := proxy.post(
		context.Background(),
		[]byte(proxyTestToolCall(3, "chat_status", `{"target":"new"}`)),
	); err != nil {
		t.Fatalf("install newer session: %v", err)
	}
	close(releaseOld)
	if err := <-oldDone; err != nil {
		t.Fatalf("complete stale response: %v", err)
	}
	session := proxy.session()
	if session.sessionID != "session-new" || session.protocol != "protocol-new" {
		t.Fatalf(
			"session after stale success = (%q, %q), want newer session",
			session.sessionID,
			session.protocol,
		)
	}
}

func TestStdioProxyCloseDoesNotClearNewerSession(t *testing.T) {
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	transport := proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodDelete {
			close(closeStarted)
			<-releaseClose
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Mcp-Session-Id": []string{"session-new"},
			},
			Body: io.NopCloser(strings.NewReader(
				`{"jsonrpc":"2.0","id":3,"result":{"protocolVersion":"protocol-new"}}`,
			)),
		}, nil
	})
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.endpoint = "http://proxy.test/mcp/chat"
	proxy.client = &http.Client{Transport: transport}
	proxy.sessionID = "session-old"
	proxy.protocol = "protocol-old"
	closed := make(chan struct{})
	go func() {
		proxy.closeSession()
		close(closed)
	}()
	<-closeStarted
	if _, err := proxy.post(
		context.Background(),
		[]byte(proxyTestToolCall(3, "chat_status", `{"target":"new"}`)),
	); err != nil {
		t.Fatalf("install session during close: %v", err)
	}
	close(releaseClose)
	<-closed
	session := proxy.session()
	if session.sessionID != "session-new" || session.protocol != "protocol-new" {
		t.Fatalf(
			"session after stale close = (%q, %q), want newer session",
			session.sessionID,
			session.protocol,
		)
	}
}
