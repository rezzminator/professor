package mcpserv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

type proxyAdmissionClock struct {
	clock.Clock
	timerCreated chan struct{}
}

func (testClock *proxyAdmissionClock) NewTimer(delay time.Duration) clock.Timer {
	timer := testClock.Clock.NewTimer(delay)
	testClock.timerCreated <- struct{}{}
	return timer
}

func TestStdioProxyAdmitsUnrelatedRequestAfterAdmissionWindow(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if string(frame.ID) == "2" {
			close(firstStarted)
			<-releaseFirst
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":`+string(frame.ID)+`,"result":{}}`)
	}))
	t.Cleanup(func() {
		close(releaseFirst)
		server.Close()
	})

	fakeClock := clock.NewFake(time.Unix(0, 0))
	testClock := &proxyAdmissionClock{Clock: fakeClock, timerCreated: make(chan struct{}, 1)}
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.clock = testClock
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "chat_status", `{"target":"seat","ask":true}`))
	<-firstStarted
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"independent"}`))

	select {
	case <-testClock.timerCreated:
	case <-time.After(time.Second):
		t.Fatal("overlapping request did not create an admission timer")
	}
	fakeClock.Advance(proxyRequestAdmissionWindow)
	response := harness.read(t)
	if response["id"] != float64(3) || response["result"] == nil {
		t.Fatalf("unrelated response before blocked request completed = %+v, want id 3 success", response)
	}
}

func TestStdioProxyAdmitsPendingRequestBeforeNextFrame(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		switch {
		case string(frame.ID) == "2":
			close(firstStarted)
			<-releaseFirst
		case string(frame.ID) == "3":
			close(secondStarted)
		case frame.Method == "notifications/progress":
			<-secondStarted
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":`+string(frame.ID)+`,"result":{}}`)
	}))

	fakeClock := clock.NewFake(time.Unix(0, 0))
	testClock := &proxyAdmissionClock{Clock: fakeClock, timerCreated: make(chan struct{}, 1)}
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.clock = testClock
	harness := startProxyTestHarness(t, proxy)
	t.Cleanup(func() {
		close(releaseFirst)
		server.Close()
	})
	harness.write(t, proxyTestToolCall(2, "chat_status", `{"target":"seat","ask":true}`))
	<-firstStarted
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"next-frame"}`))
	<-testClock.timerCreated
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/progress","params":{}}`)

	response := harness.read(t)
	if response["id"] != float64(3) || response["result"] == nil {
		t.Fatalf("pending response after next frame = %+v, want id 3 success", response)
	}
	if pending := fakeClock.Pending(); pending != 0 {
		t.Fatalf("admission timers after next frame = %d, want zero", pending)
	}
}

func TestStdioProxyForwardsCancellationWhileCallRuns(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if frame.Method == "tools/call" {
			close(started)
			<-release
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":2,"result":{}}`)
			return
		}
		if frame.Method == "notifications/cancelled" {
			close(cancelled)
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "chat_status", `{"target":"seat","ask":true}`))
	<-started
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`)
	select {
	case <-cancelled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("cancellation notification waited behind its running call")
	}
	close(release)
	_ = harness.read(t)
}

func TestStdioProxyCancelsQueuedRequestBeforeDaemonSubmission(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	daemonSawCancellation := make(chan struct{}, 1)
	var queuedCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		switch {
		case frame.Method == "tools/call" && string(frame.ID) == "2":
			close(started)
			<-release
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":2,"result":{}}`)
		case frame.Method == "tools/call" && string(frame.ID) == "3":
			queuedCalls.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":3,"result":{}}`)
		case frame.Method == "notifications/cancelled":
			daemonSawCancellation <- struct{}{}
			writer.WriteHeader(http.StatusAccepted)
		default:
			writer.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	fakeClock := clock.NewFake(time.Unix(0, 0))
	testClock := &proxyAdmissionClock{Clock: fakeClock, timerCreated: make(chan struct{}, 1)}
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.clock = testClock
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "chat_status", `{"target":"seat","ask":true}`))
	<-started
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"must-not-run"}`))
	<-testClock.timerCreated
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	select {
	case <-daemonSawCancellation:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	first, second := harness.read(t), harness.read(t)
	responses := map[float64]map[string]any{first["id"].(float64): first, second["id"].(float64): second}
	if queuedCalls.Load() != 0 {
		t.Fatalf("cancelled queued mutation reached daemon %d times, want zero", queuedCalls.Load())
	}
	if _, ok := responses[3]["error"].(map[string]any); !ok {
		t.Fatalf("cancelled queued response = %+v, want correlated JSON-RPC error", responses[3])
	}
}

func TestStdioProxyDistinguishesStringAndNumericCancellationIDs(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	cancellationForwarded := make(chan struct{})
	var queuedCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		switch {
		case frame.Method == "tools/call" && string(frame.ID) == "2":
			close(started)
			<-release
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":2,"result":{}}`)
		case frame.Method == "tools/call" && string(frame.ID) == "3":
			queuedCalls.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":3,"result":{}}`)
		case frame.Method == "notifications/cancelled":
			close(cancellationForwarded)
			writer.WriteHeader(http.StatusAccepted)
		default:
			writer.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "chat_status", `{"target":"seat","ask":true}`))
	<-started
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"numeric-id"}`))
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"3"}}`)
	select {
	case <-cancellationForwarded:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("unknown string-ID cancellation was not forwarded")
	}
	close(release)
	_ = harness.read(t)
	_ = harness.read(t)
	if queuedCalls.Load() != 1 {
		t.Fatalf("numeric request after string-ID cancellation reached daemon %d times, want once", queuedCalls.Load())
	}
}

func TestStdioProxyReusesCancelledRequestIDAfterCleanup(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var reusedCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if frame.Method != "tools/call" {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		if string(frame.ID) == "2" {
			close(started)
			<-release
		} else if string(frame.ID) == "3" {
			reusedCalls.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"jsonrpc":"2.0","id":%s,"result":{}}`, frame.ID)
	}))
	defer server.Close()

	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "chat_status", `{"target":"seat","ask":true}`))
	<-started
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"cancelled"}`))
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	close(release)
	_ = harness.read(t)
	_ = harness.read(t)

	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"reused"}`))
	response := harness.read(t)
	if _, ok := response["result"].(map[string]any); !ok {
		t.Fatalf("reused request response = %+v, want success", response)
	}
	if reusedCalls.Load() != 1 {
		t.Fatalf("reused request ID reached daemon %d times, want once", reusedCalls.Load())
	}
}

func TestStdioProxyLateCancellationPrecedesReusedRequestID(t *testing.T) {
	cancellationStarted := make(chan struct{})
	releaseCancellation := make(chan struct{})
	reusedStarted := make(chan struct{}, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if frame.Method == "notifications/cancelled" {
			close(cancellationStarted)
			<-releaseCancellation
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		if calls.Add(1) == 2 {
			reusedStarted <- struct{}{}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":3,"result":{}}`)
	}))

	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.identity = nil
	harness := startProxyTestHarness(t, proxy)
	released := false
	t.Cleanup(func() {
		if !released {
			close(releaseCancellation)
		}
		server.Close()
	})
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"first"}`))
	if response := harness.read(t); response["result"] == nil {
		t.Fatalf("first response = %+v, want success", response)
	}
	deadline := time.After(time.Second)
	for {
		proxy.requestMutex.Lock()
		active := proxy.requests["number:3"]
		tail := proxy.requestTails["number:3"]
		proxy.requestMutex.Unlock()
		if active == nil && tail == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("completed request remained registered")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	<-cancellationStarted
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"reused"}`))
	select {
	case <-reusedStarted:
		t.Fatal("reused request reached daemon before late cancellation forwarding completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCancellation)
	released = true
	select {
	case <-reusedStarted:
	case <-time.After(time.Second):
		t.Fatal("reused request did not reach daemon after late cancellation forwarding completed")
	}
	if response := harness.read(t); response["result"] == nil {
		t.Fatalf("reused response = %+v, want success", response)
	}
}
