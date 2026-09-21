package mcpserv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const proxyTestInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"proxy-test","version":"test"}}}`

type proxyTestHarness struct {
	input  *io.PipeWriter
	output *bufio.Reader
	cancel context.CancelFunc
	done   chan error
}

type proxyTestTransport func(*http.Request) (*http.Response, error)

func (transport proxyTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type proxyTestBuffer struct {
	mutex sync.Mutex
	bytes.Buffer
}

func (buffer *proxyTestBuffer) Write(content []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.Buffer.Write(content)
}

func (buffer *proxyTestBuffer) String() string {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.Buffer.String()
}

func startProxyTestHarness(t *testing.T, proxy *stdioProxy) *proxyTestHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	input, inputWriter := io.Pipe()
	output, outputWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- proxy.run(ctx, input, outputWriter)
		_ = outputWriter.Close()
	}()
	harness := &proxyTestHarness{
		input: inputWriter, output: bufio.NewReader(output), cancel: cancel, done: done,
	}
	t.Cleanup(func() {
		cancel()
		_ = inputWriter.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("stdio proxy did not stop after cancellation")
		}
	})
	return harness
}

func (harness *proxyTestHarness) write(t *testing.T, frame string) {
	t.Helper()
	if _, err := io.WriteString(harness.input, frame+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (harness *proxyTestHarness) read(t *testing.T) map[string]any {
	t.Helper()
	result := make(chan []byte, 1)
	errors := make(chan error, 1)
	go func() {
		line, err := harness.output.ReadBytes('\n')
		if err != nil {
			errors <- err
			return
		}
		result <- line
	}()
	select {
	case line := <-result:
		var frame map[string]any
		if err := json.Unmarshal(line, &frame); err != nil {
			t.Fatalf("decode proxy response %s: %v", line, err)
		}
		return frame
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for proxy response")
	}
	return nil
}

func proxyTestAddress(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "http://")
}

func proxyTestDaemon(service *Service) http.Handler {
	return NewDaemonHandler(DaemonOptions{
		Version: "test", Endpoint: "test", Chat: service.NewHTTPHandler(),
		ChatRuntimeIdentity: service.RuntimeIdentity(),
	})
}

func proxyTestService(marker string, rows []compose.Row, calls *[][]string) *Service {
	verbs := &fakeChatVerbs{
		listed: chat.ListResult{Rows: rows, Matched: len(rows)},
		last:   chat.LastResult{Text: marker + " self answer\n"}, resolveScopedSelf: true,
	}
	return newService(marker, &backend{
		chat: verbs, runtimeIdentity: "sha256:proxy-test",
		dispatch: func(_ context.Context, args []string, stdout, _ io.Writer) int {
			*calls = append(*calls, append([]string(nil), args...))
			_, _ = io.WriteString(stdout, marker+"\n")
			return 0
		},
	})
}

func proxyTestToolCall(id int, name, arguments string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) +
		`,"method":"tools/call","params":{"name":"` + name + `","arguments":` + arguments + `}}`
}

func proxyTestStructured(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	result, ok := frame["result"].(map[string]any)
	if !ok {
		t.Fatalf("proxy response has no result: %+v", frame)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("proxy response has no structured content: %+v", frame)
	}
	return structured
}

func TestStdioProxyReinitializesAfterDaemonReplacement(t *testing.T) {
	var oldCalls, newCalls [][]string
	oldService := proxyTestService("old", nil, &oldCalls)
	newService := proxyTestService("new", nil, &newCalls)
	var mutex sync.RWMutex
	current := proxyTestDaemon(oldService)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.RLock()
		handler := current
		mutex.RUnlock()
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()

	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.expectedRuntimeIdentity = "sha256:proxy-test"
	proxy.identity = nil
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestInitialize)
	_ = harness.read(t)
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	harness.write(t, proxyTestToolCall(2, "chat_new", `{"name":"before"}`))
	if got := proxyTestStructured(t, harness.read(t))["message"]; got != "old" {
		t.Fatalf("call before replacement = %v, want old", got)
	}
	mutex.Lock()
	current = proxyTestDaemon(newService)
	mutex.Unlock()
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"after"}`))
	if got := proxyTestStructured(t, harness.read(t))["message"]; got != "new" {
		t.Fatalf("call after replacement = %v, want new", got)
	}
}

func TestStdioProxyConcurrentRecoveryReinitializesOnce(t *testing.T) {
	const oldSession = "session-old"
	const recoveredSession = "session-recovered"
	firstCallsReady := make(chan struct{})
	releaseFirstCalls := make(chan struct{})
	var firstCalls atomic.Int32
	var initializeCalls atomic.Int32
	transport := proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			return nil, err
		}
		switch frame.Method {
		case proxyInitializeMethod:
			initializeCalls.Add(1)
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
			if request.Header.Get("Mcp-Session-Id") == oldSession {
				if firstCalls.Add(1) == 2 {
					close(firstCallsReady)
				}
				<-releaseFirstCalls
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

	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := proxy.sendWithRetry(
				context.Background(),
				[]byte(proxyTestToolCall(2, "chat_status", `{"target":"seat"}`)),
				false,
			)
			results <- err
		}()
	}
	<-firstCallsReady
	close(releaseFirstCalls)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent recovery: %v", err)
		}
	}
	if calls := initializeCalls.Load(); calls != 1 {
		t.Fatalf("concurrent recovery replayed initialize %d times, want once", calls)
	}
}

func TestStdioProxySynchronizesHandshakeStorageAndReplay(t *testing.T) {
	initialize := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"padding":"` +
		strings.Repeat("a", 64<<10) + `"}}`)
	initialized := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{"padding":"` +
		strings.Repeat("b", 64<<10) + `"}}`)
	alternates := [][]byte{
		bytes.ReplaceAll(initialize, []byte("a"), []byte("c")),
		bytes.ReplaceAll(initialized, []byte("b"), []byte("d")),
	}
	started := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	readDone := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var bodies [2][]byte
	var calls atomic.Int32
	transport := proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		index := int(calls.Add(1)) - 1
		var content []byte
		if index < len(started) {
			close(started[index])
			<-release[index]
			var body bytes.Buffer
			chunk := make([]byte, 8)
			for {
				count, err := request.Body.Read(chunk)
				body.Write(chunk[:count])
				runtime.Gosched()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
			}
			content = body.Bytes()
			bodies[index] = content
			close(readDone[index])
		} else {
			var err error
			if content, err = io.ReadAll(request.Body); err != nil {
				return nil, err
			}
		}
		response := &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: http.NoBody}
		if bytes.Contains(content, []byte(`"method":"initialize"`)) {
			response.Header.Set("Content-Type", "application/json")
			response.Body = io.NopCloser(strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`,
			))
		}
		return response, nil
	})
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.endpoint = "http://proxy.test/mcp/chat"
	proxy.client = &http.Client{Transport: transport}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	proxy.forward(cancelled, initialize, io.Discard)
	proxy.forward(cancelled, initialized, io.Discard)

	reinitialized := make(chan error, 2)
	for range 2 {
		go func() {
			err := proxy.reinitialize(context.Background())
			reinitialized <- err
		}()
	}
	for index := range started {
		<-started[index]
		writerReady := make(chan struct{})
		writerDone := make(chan struct{})
		go func(index int) {
			close(writerReady)
			<-release[index]
			for {
				select {
				case <-readDone[index]:
					close(writerDone)
					return
				default:
				}
				proxy.forward(cancelled, alternates[index], io.Discard)
			}
		}(index)
		<-writerReady
		close(release[index])
		<-writerDone
	}
	for range 2 {
		if err := <-reinitialized; err != nil {
			t.Fatalf("reinitialize: %v", err)
		}
	}
	for index, want := range [][]byte{initialize, initialized} {
		if !bytes.Equal(bodies[index], want) || !json.Valid(bodies[index]) {
			t.Fatalf("replayed handshake frame %d was incomplete", index+1)
		}
	}
	proxy.handshakeMutex.Lock()
	proxy.initialized = nil
	proxy.handshakeMutex.Unlock()
	before := calls.Load()
	if err := proxy.reinitialize(context.Background()); err != nil || calls.Load() != before+1 {
		t.Fatalf("initialize-only replay: calls=%d err=%v", calls.Load()-before, err)
	}
}

func TestStdioProxyBoundsUnavailableDaemonPerRequest(t *testing.T) {
	var calls [][]string
	service := proxyTestService("initial", nil, &calls)
	server := httptest.NewServer(proxyTestDaemon(service))
	address := proxyTestAddress(server)
	proxy := newStdioProxy(context.Background(), address, io.Discard)
	proxy.retryWindow = 120 * time.Millisecond
	proxy.retryDelay = 10 * time.Millisecond
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestInitialize)
	_ = harness.read(t)
	server.Close()

	started := time.Now()
	harness.write(t, proxyTestToolCall(7, "chat_new", `{"name":"down"}`))
	response := harness.read(t)
	if response["id"] != float64(7) {
		t.Fatalf("bounded error id = %#v, want 7", response["id"])
	}
	errorObject, ok := response["error"].(map[string]any)
	if !ok {
		t.Fatalf("bounded response = %+v, want JSON-RPC error", response)
	}
	message, _ := errorObject["message"].(string)
	for _, part := range []string{"pfm MCP daemon", address, "120ms", "pfm mcp serve"} {
		if !strings.Contains(message, part) {
			t.Errorf("bounded error %q does not name %q", message, part)
		}
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded call took %v, want under one second", elapsed)
	}
}

func TestStdioProxyDoesNotReplayMutationAfterResponseEOF(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack response: %v", err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.retryWindow = 100 * time.Millisecond
	proxy.retryDelay = time.Millisecond

	_, err := proxy.sendWithRetry(
		context.Background(),
		[]byte(proxyTestToolCall(2, "chat_new", `{"name":"child"}`)),
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "was not replayed") {
		t.Fatalf("EOF mutation error = %v, want the no-replay verdict", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("EOF mutation reached the daemon %d times, want once", calls.Load())
	}
}

func TestStdioProxyDoesNotReplayMutationAfterInvalidSuccessResponse(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "malformed JSON", contentType: "application/json", body: `{"jsonrpc":`},
		{name: "unsupported content type", contentType: "text/plain", body: `accepted`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				writer.Header().Set("Content-Type", test.contentType)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
			proxy.retryWindow = 50 * time.Millisecond
			proxy.retryDelay = time.Millisecond

			_, err := proxy.sendWithRetry(
				context.Background(),
				[]byte(proxyTestToolCall(2, "chat_new", `{"name":"child"}`)),
				false,
			)
			if err == nil || !strings.Contains(err.Error(), "was not replayed") {
				t.Fatalf("invalid success response error = %v, want the no-replay verdict", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("mutation reached daemon %d times, want once", calls.Load())
			}
		})
	}
}

func TestStdioProxyAllowsHealthyCallsPastRetryWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		<-time.After(100 * time.Millisecond)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":2,"result":{}}`)
	}))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	if proxy.client.Timeout != 0 {
		t.Fatalf("proxy client timeout = %s, want tool execution governed by its request context", proxy.client.Timeout)
	}
	proxy.retryWindow = 30 * time.Millisecond
	if _, err := proxy.sendWithRetry(
		context.Background(),
		[]byte(proxyTestToolCall(2, "chat_status", `{"target":"seat","ask":true}`)),
		false,
	); err != nil {
		t.Fatalf("healthy request beyond the retry window failed: %v", err)
	}
}

func TestStdioProxyAllowsExistingMaximumCaptureResponse(t *testing.T) {
	service := newService("test", &backend{chat: &fakeChatVerbs{
		last: chat.LastResult{Text: strings.Repeat("x", maxCaptureBytes)},
	}})
	server := httptest.NewServer(proxyTestDaemon(service))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	if _, err := proxy.post(context.Background(), []byte(proxyTestInitialize)); err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.post(
		context.Background(),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.post(
		context.Background(),
		[]byte(proxyTestToolCall(2, "chat_last", `{"target":"seat"}`)),
	); err != nil {
		t.Fatalf("maximum capture-sized response failed: %v", err)
	}
}

func TestStdioProxyAllowsEscapedMaximumCaptureResponse(t *testing.T) {
	service := newService("test", &backend{chat: &fakeChatVerbs{
		last: chat.LastResult{Text: strings.Repeat("<", maxCaptureBytes)},
	}})
	server := httptest.NewServer(proxyTestDaemon(service))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	if _, err := proxy.post(context.Background(), []byte(proxyTestInitialize)); err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.post(
		context.Background(),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.post(
		context.Background(),
		[]byte(proxyTestToolCall(2, "chat_last", `{"target":"seat"}`)),
	); err != nil {
		t.Fatalf("escaped maximum capture-sized response failed: %v", err)
	}
}

func TestStdioProxyEnrichesSplitCallerWithoutExportedID(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "b1111111-1111-4111-8111-111111111111"
	transcriptDir := filepath.Join(root, "claude", "project")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(transcriptDir, id+".jsonl")
	if err := os.WriteFile(
		transcript,
		[]byte(`{"type":"user","cwd":"/work/project","message":{"content":"split caller"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "tmux", "cc-proxy-split")
	command := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "proxy-split", "sleep 120",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	if err := os.WriteFile(
		filepath.Join(root, "sid", "cc-proxy-split.%0"),
		[]byte(transcript+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%0")
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
	var warnings proxyTestBuffer
	proxy := newStdioProxy(context.Background(), "unused", &warnings)
	if proxy.identity == nil || proxy.identity.ID != id || proxy.identity.Pane != "%0" {
		t.Fatalf(
			"proxy identity = %+v, want transcript %q on pane %%0; warnings: %s",
			proxy.identity,
			id,
			warnings.String(),
		)
	}
}

func TestStdioProxyWaitsForDaemonInsideRetryWindow(t *testing.T) {
	var initialCalls, returnedCalls [][]string
	initial := proxyTestDaemon(proxyTestService("initial", nil, &initialCalls))
	returned := proxyTestDaemon(proxyTestService("returned", nil, &returnedCalls))
	var mutex sync.RWMutex
	current := initial
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.RLock()
		handler := current
		mutex.RUnlock()
		if handler == nil {
			http.Error(writer, "daemon restarting", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()

	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.expectedRuntimeIdentity = "sha256:proxy-test"
	proxy.identity = nil
	proxy.retryWindow = 500 * time.Millisecond
	proxy.retryDelay = 10 * time.Millisecond
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestInitialize)
	_ = harness.read(t)
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	mutex.Lock()
	current = nil
	mutex.Unlock()
	restored := make(chan struct{})
	go func() {
		<-time.After(50 * time.Millisecond)
		mutex.Lock()
		current = returned
		mutex.Unlock()
		close(restored)
	}()
	harness.write(t, proxyTestToolCall(8, "chat_new", `{"name":"after-restart"}`))
	if got := proxyTestStructured(t, harness.read(t))["message"]; got != "returned" {
		t.Fatalf("call after retry-window recovery = %v, want returned", got)
	}
	<-restored
}

func TestStdioProxyRefusesReplayIntoDifferentRuntime(t *testing.T) {
	var routeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/status" {
			identity := "sha256:selected"
			if routeCalls.Load() > 0 {
				identity = "sha256:replacement"
			}
			if err := json.NewEncoder(writer).Encode(DaemonStatus{
				PID: 1, Servers: map[string][]string{"chat": ToolNames()}, ChatRuntimeIdentity: identity,
			}); err != nil {
				t.Errorf("encode status: %v", err)
			}
			return
		}
		routeCalls.Add(1)
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.expectedRuntimeIdentity = "sha256:selected"
	proxy.sessionID = "old-session"
	proxy.protocol = "2025-06-18"
	proxy.retryDelay = time.Millisecond
	proxy.retryWindow = time.Second
	proxy.storeHandshake(proxyInitializeMethod, []byte(proxyTestInitialize))
	_, err := proxy.sendWithRetry(
		context.Background(), []byte(proxyTestToolCall(8, "chat_new", `{"name":"wrong-fleet"}`)), false,
	)
	if err == nil || !strings.Contains(err.Error(), "runtime mismatch") ||
		!strings.Contains(err.Error(), "not replayed") {
		t.Fatalf("mismatched replacement error = %v, want contextual no-replay error", err)
	}
	if routeCalls.Load() != 1 {
		t.Fatalf("mismatched replacement received %d route posts, want only failed original", routeCalls.Load())
	}
}

func TestStdioProxyDropsUndeliverableNotification(t *testing.T) {
	var calls [][]string
	service := proxyTestService("initial", nil, &calls)
	server := httptest.NewServer(proxyTestDaemon(service))
	address := proxyTestAddress(server)
	var warnings proxyTestBuffer
	proxy := newStdioProxy(context.Background(), address, &warnings)
	proxy.retryWindow = 80 * time.Millisecond
	proxy.retryDelay = 10 * time.Millisecond
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestInitialize)
	_ = harness.read(t)
	server.Close()

	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":44}}`)
	deadline := time.After(time.Second)
	for !strings.Contains(warnings.String(), "dropped notification") {
		select {
		case <-deadline:
			t.Fatalf("notification warning = %q", warnings.String())
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestStdioProxyCarriesCallerIdentityEndToEnd(t *testing.T) {
	row := compose.Row{
		SessionName: "cc-seat", ID: "session-id", CWD: "/work/proxy", Project: "proxy",
		Name: "Proxy Claude", Kind: compose.LiveClaude, Socket: "cc-seat", PaneID: "%7",
	}
	var calls [][]string
	service := proxyTestService("daemon", []compose.Row{row}, &calls)
	server := httptest.NewServer(proxyTestDaemon(service))
	defer server.Close()

	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.sidDir = t.TempDir()
	proxy.identity = &ProxyIdentity{
		Version: ProxyWireVersion, Session: "cc-seat", SocketPath: "/tmp/tmux-1000/cc-seat",
		SocketName: "cc-seat", Pane: "%7", Engine: "claude", ID: "session-id", Source: "tmux",
	}
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestInitialize)
	_ = harness.read(t)
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	harness.write(t, proxyTestToolCall(2, "chat_whoami", `{}`))
	whoami := proxyTestStructured(t, harness.read(t))
	if whoami["status"] != "ok" || whoami["session"] != "cc-seat" || whoami["id"] != "session-id" {
		t.Fatalf("proxied chat_whoami = %+v", whoami)
	}
	harness.write(t, proxyTestToolCall(3, "chat_last", `{"target":"self"}`))
	last := proxyTestStructured(t, harness.read(t))
	if last["text"] != "daemon self answer" {
		t.Fatalf("proxied self call = %+v", last)
	}
	harness.write(t, proxyTestToolCall(4, "chat_new", `{"name":"child"}`))
	_ = proxyTestStructured(t, harness.read(t))
	wantCalls := [][]string{{"chat", "new", "--name", "child", "--engine", "cc", "--cwd", "/work/proxy"}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("proxied chat_new calls = %q, want %q", calls, wantCalls)
	}
}

func TestStdioProxyRefreshesClaudeConversationForEveryCall(t *testing.T) {
	var received []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame map[string]any
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		received = append(received, frame)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":2,"result":{}}`)
	}))
	defer server.Close()

	sidDir := t.TempDir()
	const socket = "cc-1-2-3"
	crumb := filepath.Join(sidDir, socket+".%1")
	writeCrumb := func(id string) {
		t.Helper()
		if err := os.WriteFile(crumb, []byte("/transcripts/"+id+".jsonl\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCrumb("conversation-a")
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.sidDir = sidDir
	proxy.identity = &ProxyIdentity{
		Version: ProxyWireVersion, Session: "seat", SocketName: socket,
		Pane: "%1", Engine: "claude", ID: "conversation-a",
	}
	var output bytes.Buffer
	proxy.forward(context.Background(), []byte(proxyTestToolCall(2, "chat_whoami", `{}`)), &output)
	writeCrumb("conversation-b")
	proxy.forward(context.Background(), []byte(proxyTestToolCall(3, "chat_whoami", `{}`)), &output)
	if len(received) != 2 {
		t.Fatalf("daemon received %d calls, want 2", len(received))
	}
	for index, want := range []string{"conversation-a", "conversation-b"} {
		params := received[index]["params"].(map[string]any)
		meta := params["_meta"].(map[string]any)
		identity := meta["pfmProxy"].(map[string]any)
		if identity["id"] != want {
			t.Fatalf("call %d proxy id = %v, want current crumb %q", index+1, identity["id"], want)
		}
	}
	if err := os.Remove(crumb); err != nil {
		t.Fatal(err)
	}
	proxy.forward(context.Background(), []byte(proxyTestToolCall(4, "chat_whoami", `{}`)), &output)
	missingParams := received[2]["params"].(map[string]any)
	missingMeta := missingParams["_meta"].(map[string]any)
	missingIdentity := missingMeta["pfmProxy"].(map[string]any)
	if missingIdentity["id"] != "" {
		t.Fatalf("missing current crumb replayed cached id: %+v", missingIdentity)
	}
	if err := os.Mkdir(crumb, 0o700); err != nil {
		t.Fatal(err)
	}
	before := len(received)
	proxy.forward(context.Background(), []byte(proxyTestToolCall(5, "chat_whoami", `{}`)), &output)
	if len(received) != before || !strings.Contains(output.String(), "refresh Claude caller transcript") {
		t.Fatalf(
			"crumb read failure forwarded mutation or lacked named error: received=%d output=%s",
			len(received),
			output.String(),
		)
	}
	if err := os.Remove(crumb); err != nil {
		t.Fatal(err)
	}
	writeCrumb("conversation-c")
	explicit := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"_meta":{"threadId":"thread-explicit"},"name":"chat_whoami","arguments":{}}}`
	proxy.forward(context.Background(), []byte(explicit), &output)
	explicitParams := received[len(received)-1]["params"].(map[string]any)
	explicitMeta := explicitParams["_meta"].(map[string]any)
	if explicitMeta["threadId"] != "thread-explicit" {
		t.Fatalf("explicit thread metadata changed: %+v", explicitMeta)
	}
	explicitIdentity := explicitMeta["pfmProxy"].(map[string]any)
	if explicitIdentity["id"] != "conversation-a" {
		t.Fatalf("explicit thread metadata triggered refresh: %+v", explicitIdentity)
	}
	proxy.forward(context.Background(), []byte(proxyTestToolCall(7, "chat_whoami", `{}`)), &output)
	recoveredParams := received[len(received)-1]["params"].(map[string]any)
	recoveredMeta := recoveredParams["_meta"].(map[string]any)
	recoveredIdentity := recoveredMeta["pfmProxy"].(map[string]any)
	if recoveredIdentity["id"] != "conversation-c" {
		t.Fatalf("pipe did not recover after crumb read error: %+v", recoveredIdentity)
	}
}

func TestStdioProxyLeavesUnresolvedIdentityForDaemonToRefuse(t *testing.T) {
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
	harness.write(t, proxyTestToolCall(2, "chat_whoami", `{}`))
	whoami := proxyTestStructured(t, harness.read(t))
	message, _ := whoami["message"].(string)
	if whoami["status"] != "not_found" || !strings.Contains(message, "cannot derive who is calling") {
		t.Fatalf("unresolved proxy identity = %+v, want the daemon's named refusal", whoami)
	}
}

func TestStdioProxyClosesDaemonSessionAtEOF(t *testing.T) {
	var calls [][]string
	daemon := proxyTestDaemon(proxyTestService("daemon", nil, &calls))
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deletes.Add(1)
		}
		daemon.ServeHTTP(writer, request)
	}))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	var output bytes.Buffer
	if err := proxy.run(
		context.Background(),
		strings.NewReader(proxyTestInitialize+"\n"),
		&output,
	); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("daemon session DELETE count = %d, want 1", deletes.Load())
	}
}

func TestStdioProxyCancelsOutstandingCallAtEOF(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if frame.Method != "tools/call" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		close(started)
		<-request.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	input, inputWriter := io.Pipe()
	returned := make(chan error, 1)
	go func() { returned <- proxy.run(context.Background(), input, io.Discard) }()
	if _, err := io.WriteString(inputWriter, proxyTestToolCall(2, "chat_whoami", `{}`)+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("outstanding proxy call did not start")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("proxy EOF returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy EOF did not cancel its outstanding call")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon request context survived proxy EOF")
	}
}
