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
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// proxyAdmissionClock puts a request's admission window on the fake a test
// advances, signalling each one it creates, and parks every other timer (a
// retry window's bound) on a fake of its own, so the test's fake counts
// admission windows alone.
type proxyAdmissionClock struct {
	clock.Clock
	bounds       *clock.Fake
	timerCreated chan struct{}
}

func newProxyAdmissionClock(admission *clock.Fake) *proxyAdmissionClock {
	return &proxyAdmissionClock{
		Clock: admission, bounds: clock.NewFake(admission.Now()), timerCreated: make(chan struct{}, 1),
	}
}

func (testClock *proxyAdmissionClock) NewTimer(delay time.Duration) clock.Timer {
	if delay != proxyRequestAdmissionWindow {
		return testClock.bounds.NewTimer(delay)
	}
	timer := testClock.Clock.NewTimer(delay)
	testClock.timerCreated <- struct{}{}
	return timer
}

func TestStdioProxyAdmitsUnrelatedRequestAfterAdmissionWindow(t *testing.T) {
	t.Parallel()
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
	testClock := newProxyAdmissionClock(fakeClock)
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.clock = testClock
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "status", `{"target":"seat","ask":true}`))
	<-firstStarted
	harness.write(t, proxyTestToolCall(3, "new", `{"name":"independent"}`))

	testwait.Receive(t, "the overlapping request to create an admission timer", testClock.timerCreated)
	fakeClock.Advance(proxyRequestAdmissionWindow)
	response := harness.read(t)
	if response["id"] != float64(3) || response["result"] == nil {
		t.Fatalf("unrelated response before blocked request completed = %+v, want id 3 success", response)
	}
}

func TestStdioProxyAdmitsPendingRequestBeforeNextFrame(t *testing.T) {
	t.Parallel()
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
	testClock := newProxyAdmissionClock(fakeClock)
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.clock = testClock
	harness := startProxyTestHarness(t, proxy)
	t.Cleanup(func() {
		close(releaseFirst)
		server.Close()
	})
	harness.write(t, proxyTestToolCall(2, "status", `{"target":"seat","ask":true}`))
	<-firstStarted
	harness.write(t, proxyTestToolCall(3, "new", `{"name":"next-frame"}`))
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
	t.Parallel()
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
	proxyTestStepping(proxy)
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "status", `{"target":"seat","ask":true}`))
	<-started
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`)
	// The call stays held until release, so a cancellation queued behind it
	// would never arrive: the wait ends only on the cancellation passing it.
	testwait.Receive(t, "the cancellation notification to pass its running call", cancelled)
	close(release)
	_ = harness.read(t)
}

func TestStdioProxyCancelsQueuedRequestBeforeDaemonSubmission(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
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
		default:
			writer.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	fakeClock := clock.NewFake(time.Unix(0, 0))
	testClock := newProxyAdmissionClock(fakeClock)
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.clock = testClock
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "status", `{"target":"seat","ask":true}`))
	<-started
	harness.write(t, proxyTestToolCall(3, "new", `{"name":"must-not-run"}`))
	<-testClock.timerCreated
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	testwait.Until(t, "the queued request withdrawn by its cancellation", func() (bool, string) {
		proxy.requestMutex.Lock()
		defer proxy.requestMutex.Unlock()
		queued := proxy.requestTails["number:3"]
		return queued == nil, fmt.Sprintf("queued request 3 = %+v", queued)
	})
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
	t.Parallel()
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
	proxyTestStepping(proxy)
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "status", `{"target":"seat","ask":true}`))
	<-started
	harness.write(t, proxyTestToolCall(3, "new", `{"name":"numeric-id"}`))
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"3"}}`)
	testwait.Receive(t, "the unknown string-ID cancellation to be forwarded", cancellationForwarded)
	close(release)
	_ = harness.read(t)
	_ = harness.read(t)
	if queuedCalls.Load() != 1 {
		t.Fatalf("numeric request after string-ID cancellation reached daemon %d times, want once", queuedCalls.Load())
	}
}

func TestStdioProxyReusesCancelledRequestIDAfterCleanup(t *testing.T) {
	t.Parallel()
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

	// The queued request's admission window stays open until its cancellation is read: a window that
	// passed at once would admit request 3 to the daemon whenever its goroutine ran before the reader.
	testClock := newProxyAdmissionClock(clock.NewFake(time.Unix(0, 0)))
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.clock = testClock
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "status", `{"target":"seat","ask":true}`))
	<-started
	harness.write(t, proxyTestToolCall(3, "new", `{"name":"cancelled"}`))
	testwait.Receive(t, "the queued request to arm its admission window", testClock.timerCreated)
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	close(release)
	_ = harness.read(t)
	_ = harness.read(t)
	testwait.Until(t, "both requests to leave the registry", func() (bool, string) {
		proxy.requestMutex.Lock()
		defer proxy.requestMutex.Unlock()
		return len(proxy.requestTails) == 0, fmt.Sprintf("tails %+v", proxy.requestTails)
	})

	harness.write(t, proxyTestToolCall(3, "new", `{"name":"reused"}`))
	response := harness.read(t)
	if _, ok := response["result"].(map[string]any); !ok {
		t.Fatalf("reused request response = %+v, want success", response)
	}
	if reusedCalls.Load() != 1 {
		t.Fatalf("reused request ID reached daemon %d times, want once", reusedCalls.Load())
	}
}

func TestStdioProxyLateCancellationPrecedesReusedRequestID(t *testing.T) {
	t.Parallel()
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

	fakeClock := clock.NewFake(time.Unix(0, 0))
	testClock := newProxyAdmissionClock(fakeClock)
	proxy := newStdioProxy(context.Background(), proxyTestAddress(server), io.Discard)
	proxy.clock = testClock
	proxy.identify = func(context.Context) (resolve.Identity, error) { return resolve.Identity{}, resolve.ErrNotInChat }
	harness := startProxyTestHarness(t, proxy)
	released := false
	t.Cleanup(func() {
		if !released {
			close(releaseCancellation)
		}
		server.Close()
	})
	harness.write(t, proxyTestToolCall(3, "new", `{"name":"first"}`))
	if response := harness.read(t); response["result"] == nil {
		t.Fatalf("first response = %+v, want success", response)
	}
	testwait.Until(t, "the completed request to leave the registry", func() (bool, string) {
		proxy.requestMutex.Lock()
		defer proxy.requestMutex.Unlock()
		active, tail := proxy.requests["number:3"], proxy.requestTails["number:3"]
		return active == nil && tail == nil, fmt.Sprintf("active %+v, tail %+v", active, tail)
	})

	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	<-cancellationStarted
	harness.write(t, proxyTestToolCall(3, "new", `{"name":"reused"}`))
	testwait.Receive(t, "the reused request to arm its admission timer", testClock.timerCreated)
	fakeClock.Advance(proxyRequestAdmissionWindow)
	// Presence beside the absence: the reused request is admitted and blocked
	// on the late cancellation, the one point it would forward from.
	reached := false
	held := "the reused request blocked behind the late cancellation, or at the daemon"
	testwait.Until(t, held, func() (bool, string) {
		if len(reusedStarted) != 0 {
			reached = true
			return true, "at the daemon"
		}
		return testwait.Blocked("mcpserv.waitForProxyRequest", "mcpserv.(*stdioProxy).forwardRequest")
	})
	if reached {
		t.Fatal("reused request reached daemon before late cancellation forwarding completed")
	}
	close(releaseCancellation)
	released = true
	testwait.Receive(
		t,
		"the reused request to reach the daemon after late cancellation forwarding completed",
		reusedStarted,
	)
	if response := harness.read(t); response["result"] == nil {
		t.Fatalf("reused response = %+v, want success", response)
	}
}
