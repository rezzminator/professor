package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

type proxyCancellationClock struct {
	clock.Clock
	timerCreated chan struct{}
}

func (testClock *proxyCancellationClock) NewTimer(delay time.Duration) clock.Timer {
	timer := testClock.Clock.NewTimer(delay)
	testClock.timerCreated <- struct{}{}
	return timer
}

func TestStdioProxyCancellationStopsUndeliveredRetry(t *testing.T) {
	t.Parallel()
	firstAttempt := make(chan struct{})
	cancellationForwarded := make(chan struct{})
	var attempts atomic.Int32
	var mutations atomic.Int32
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.identity = nil
	proxy.retryDelay = 200 * time.Millisecond
	proxy.retryWindow = time.Second
	proxy.client = &http.Client{Transport: proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			return nil, err
		}
		if frame.Method == "notifications/cancelled" {
			close(cancellationForwarded)
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: http.NoBody}, nil
		}
		if attempts.Add(1) == 1 {
			close(firstAttempt)
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
		}
		mutations.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":2,"result":{}}`)),
		}, nil
	})}
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "chat_new", `{"name":"cancelled-child"}`))
	<-firstAttempt
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`)
	<-cancellationForwarded
	_ = harness.read(t)
	if mutations.Load() != 0 {
		t.Fatalf("cancelled request still executed %d mutations after reconnect", mutations.Load())
	}
}

func TestStdioProxyCancellationWinsAtRetryReinitializationBoundary(t *testing.T) {
	t.Parallel()
	firstAttempt := make(chan struct{})
	cancellationForwarded := make(chan struct{})
	fakeClock := clock.NewFake(time.Unix(0, 0))
	testClock := &proxyCancellationClock{Clock: fakeClock, timerCreated: make(chan struct{}, 1)}
	var attempts atomic.Int32
	var mutations atomic.Int32
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.identity = nil
	proxy.clock = testClock
	proxy.retryDelay = time.Second
	proxy.retryWindow = time.Minute
	// A held session lets the first attempt reach the daemon before any replay.
	proxy.sessionID = "session-before-refusal"
	proxy.protocol = "2025-06-18"
	proxy.storeHandshake(proxyInitializeMethod, []byte(proxyTestInitialize))
	proxy.client = &http.Client{Transport: proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			return nil, err
		}
		if frame.Method == "notifications/cancelled" {
			close(cancellationForwarded)
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: http.NoBody}, nil
		}
		if attempts.Add(1) == 1 {
			close(firstAttempt)
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
		}
		if frame.Method == "tools/call" {
			mutations.Add(1)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`,
			)),
		}, nil
	})}

	proxy.reinitMutex.Lock()
	reinitLocked := true
	t.Cleanup(func() {
		if reinitLocked {
			proxy.reinitMutex.Unlock()
		}
	})
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(2, "chat_new", `{"name":"cancelled-at-boundary"}`))
	<-firstAttempt
	<-testClock.timerCreated
	fakeClock.Advance(proxy.retryDelay)
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`)
	// The cancellation needs a session too, so it waits behind the held replay;
	// the request must already be cancelled when the replay boundary opens.
	deadline := time.After(2 * time.Second)
	for {
		proxy.requestMutex.Lock()
		request := proxy.requests["number:2"]
		cancelled := request != nil && request.cancelled
		proxy.requestMutex.Unlock()
		if cancelled {
			break
		}
		select {
		case <-deadline:
			t.Fatal("request 2 was never marked cancelled")
		case <-time.After(time.Millisecond):
		}
	}
	proxy.reinitMutex.Unlock()
	reinitLocked = false
	<-cancellationForwarded
	_ = harness.read(t)
	if mutations.Load() != 0 {
		t.Fatalf("cancelled request executed %d mutations after its retry timer fired", mutations.Load())
	}
}

func TestStdioProxyReusesCancelledStartedRequestID(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	cancellationStarted := make(chan struct{})
	releaseCancellation := make(chan struct{})
	reusedStarted := make(chan struct{}, 1)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCancellation) }) }
	t.Cleanup(release)
	var calls atomic.Int32
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.identity = nil
	proxy.sessionID = "session-current"
	proxy.protocol = "2025-06-18"
	proxy.sessionGeneration = 7
	proxy.client = &http.Client{Transport: proxyTestTransport(func(request *http.Request) (*http.Response, error) {
		var frame proxyFrame
		if err := json.NewDecoder(request.Body).Decode(&frame); err != nil {
			return nil, err
		}
		if frame.Method == "notifications/cancelled" {
			close(cancellationStarted)
			<-releaseCancellation
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: http.NoBody}, nil
		}
		if calls.Add(1) == 1 {
			close(started)
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		reusedStarted <- struct{}{}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":3,"result":{}}`)),
		}, nil
	})}
	harness := startProxyTestHarness(t, proxy)
	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"cancelled"}`))
	<-started
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	<-cancellationStarted
	if response := harness.read(t); response["id"] != float64(3) || response["error"] == nil {
		t.Fatalf("cancelled started response = %+v, want correlated error", response)
	}
	if session := proxy.session(); session.sessionID != "session-current" || session.generation != 7 {
		t.Fatalf("session after request cancellation = %+v, want current session unchanged", session)
	}

	harness.write(t, proxyTestToolCall(3, "chat_new", `{"name":"reused"}`))
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	select {
	case <-reusedStarted:
		t.Fatal("reused request reached daemon before the old cancellation notification completed")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-reusedStarted:
	case <-time.After(time.Second):
		t.Fatal("reused request did not reach daemon after the old cancellation notification completed")
	}
	if response := harness.read(t); response["id"] != float64(3) || response["result"] == nil {
		t.Fatalf("reused started request response = %+v, want success", response)
	}
	if calls.Load() != 2 {
		t.Fatalf("started request ID reached daemon %d times, want cancelled and reused calls", calls.Load())
	}
}

// TestStdioProxyCancellationInterruptsAnUnresponsiveReplayProbe pins that the
// runtime probe before a replay honours the caller's cancellation: a daemon
// that accepts the connection and never answers must not hold a cancelled
// request for the probe's whole deadline.
func TestStdioProxyCancellationInterruptsAnUnresponsiveReplayProbe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close silent daemon: %v", err)
		}
	})
	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, acceptErr := listener.Accept(); acceptErr == nil {
			accepted <- conn
		}
	}()
	previous := DaemonProbeTimeoutOverride
	DaemonProbeTimeoutOverride = 30 * time.Second
	t.Cleanup(func() { DaemonProbeTimeoutOverride = previous })
	proxy := newStdioProxy(context.Background(), listener.Addr().String(), io.Discard)
	proxy.identity = nil
	proxy.expectedRuntimeIdentity = "sha256:proxy-test"
	proxy.retryDelay = time.Millisecond
	proxy.retryWindow = time.Minute
	proxy.sessionID = "session-before-refusal"
	proxy.protocol = "2025-06-18"
	proxy.storeHandshake(proxyInitializeMethod, []byte(proxyTestInitialize))
	proxy.client = &http.Client{Transport: proxyTestTransport(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, sendErr := proxy.sendWithRetry(ctx, []byte(proxyTestToolCall(2, "chat_ls", `{}`)), false)
		done <- sendErr
	}()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("the replay never probed the daemon's runtime")
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close probe connection: %v", err)
		}
	}()
	cancel()
	select {
	case sendErr := <-done:
		if !errors.Is(sendErr, context.Canceled) {
			t.Fatalf("sendWithRetry() error = %v, want context.Canceled", sendErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled request stayed blocked on the unresponsive daemon's runtime probe")
	}
}

func TestRunStdioCancelledDaemonProbe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close silent daemon: %v", err)
		}
	})
	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, acceptErr := listener.Accept(); acceptErr == nil {
			accepted <- conn
		}
	}()
	previous := DaemonProbeTimeoutOverride
	DaemonProbeTimeoutOverride = 30 * time.Second
	t.Cleanup(func() { DaemonProbeTimeoutOverride = previous })
	var warnings proxyTestBuffer
	service := stdioTestService("cancelled-start", &warnings)
	professor := stdioTestProfessor(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outputReader, outputWriter := io.Pipe()
	defer func() {
		_ = outputReader.Close()
		_ = outputWriter.Close()
	}()
	done := make(chan error, 1)
	go func() {
		done <- professor.runStdioTransport(
			ctx, io.NopCloser(strings.NewReader("")), outputWriter,
			stdioTestOptions(service, listener.Addr().String()),
		)
	}()
	var conn net.Conn
	finished := false
	defer func() {
		cancel()
		if conn != nil {
			if err := conn.Close(); err != nil {
				t.Errorf("close start probe connection: %v", err)
			}
		}
		if !finished {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("stdio start stayed blocked after closing the probe")
			}
		}
	}()
	select {
	case conn = <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("stdio start never probed the daemon")
	}
	cancel()
	select {
	case err := <-done:
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Errorf("stdio start error = %v, want context.Canceled", err)
		}
		if strings.Contains(warnings.String(), "using in-process MCP") {
			t.Errorf("cancelled stdio start fell back: %s", warnings.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled stdio start stayed blocked on the daemon probe")
	}
}
