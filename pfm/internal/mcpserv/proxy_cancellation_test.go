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
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

type proxyCancellationClock struct {
	clock.Clock
	window       time.Duration // the retry window's own timer, which signals nothing
	timerCreated chan struct{}
}

func (testClock *proxyCancellationClock) NewTimer(delay time.Duration) clock.Timer {
	timer := testClock.Clock.NewTimer(delay)
	if delay != testClock.window {
		testClock.timerCreated <- struct{}{}
	}
	return timer
}

func TestStdioProxyCancellationStopsUndeliveredRetry(t *testing.T) {
	t.Parallel()
	firstAttempt := make(chan struct{})
	cancellationForwarded := make(chan struct{})
	var attempts atomic.Int32
	var mutations atomic.Int32
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.identify = func(context.Context) (resolve.Identity, error) { return resolve.Identity{}, resolve.ErrNotInChat }
	// The retry delay never passes on its own: the cancellation must end it.
	proxy.clock = admittingClock{clock.NewFake(time.Unix(0, 0))}
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
	harness.write(t, proxyTestToolCall(2, "new", `{"name":"cancelled-child"}`))
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
	testClock := &proxyCancellationClock{Clock: fakeClock, window: time.Minute, timerCreated: make(chan struct{}, 1)}
	var attempts atomic.Int32
	var mutations atomic.Int32
	proxy := newStdioProxy(context.Background(), "unused", io.Discard)
	proxy.identify = func(context.Context) (resolve.Identity, error) { return resolve.Identity{}, resolve.ErrNotInChat }
	proxy.clock = testClock
	proxy.retryDelay = time.Second
	proxy.retryWindow = time.Minute
	// A held session lets the first attempt reach the daemon before any replay.
	proxy.sessionID = "session-before-refusal"
	proxy.protocol = "2025-06-18"
	proxy.handshake.store(proxyInitializeMethod, []byte(proxyTestInitialize))
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
	harness.write(t, proxyTestToolCall(2, "new", `{"name":"cancelled-at-boundary"}`))
	<-firstAttempt
	<-testClock.timerCreated
	fakeClock.Advance(proxy.retryDelay)
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`)
	// The cancellation needs a session too, so it waits behind the held replay;
	// the request must already be cancelled when the replay boundary opens. The
	// retry loop reads the request's context, which is cancelled just after the
	// cancelled flag is set, so the test waits on the context.
	testwait.Until(t, "request 2 to be marked cancelled", func() (bool, string) {
		proxy.requestMutex.Lock()
		defer proxy.requestMutex.Unlock()
		request := proxy.requests["number:2"]
		return request != nil && request.ctx.Err() != nil, "request 2 is not cancelled"
	})
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
	proxyTestStepping(proxy)
	proxy.identify = func(context.Context) (resolve.Identity, error) { return resolve.Identity{}, resolve.ErrNotInChat }
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
	harness.write(t, proxyTestToolCall(3, "new", `{"name":"cancelled"}`))
	<-started
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	<-cancellationStarted
	if response := harness.read(t); response["id"] != float64(3) || response["error"] == nil {
		t.Fatalf("cancelled started response = %+v, want correlated error", response)
	}
	if session := proxy.session(); session.sessionID != "session-current" || session.generation != 7 {
		t.Fatalf("session after request cancellation = %+v, want current session unchanged", session)
	}

	harness.write(t, proxyTestToolCall(3, "new", `{"name":"reused"}`))
	harness.write(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	// Presence beside the absence: the reused request is blocked behind the old
	// cancellation notification, the one point it would forward from.
	reached := false
	testwait.Until(
		t,
		"the reused request blocked behind the old cancellation, or at the daemon",
		func() (bool, string) {
			if len(reusedStarted) != 0 {
				reached = true
				return true, "at the daemon"
			}
			return testwait.Blocked("mcpserv.waitForProxyRequest", "mcpserv.(*stdioProxy).forwardRequest")
		},
	)
	if reached {
		t.Fatal("reused request reached daemon before the old cancellation notification completed")
	}
	release()
	testwait.Receive(
		t,
		"the reused request to reach the daemon after the old cancellation notification completed",
		reusedStarted,
	)
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
	proxy := newStdioProxy(context.Background(), listener.Addr().String(), io.Discard)
	// The replay probe's deadline sits on a fake that never reaches it: only
	// the cancellation ends the probe.
	fake := clock.NewFake(time.Unix(0, 0))
	proxy.clock = fake
	proxy.identify = func(context.Context) (resolve.Identity, error) { return resolve.Identity{}, resolve.ErrNotInChat }
	proxy.expectedRuntimeIdentity = "sha256:proxy-test"
	proxy.sessionID = "session-before-refusal"
	proxy.protocol = "2025-06-18"
	proxy.handshake.store(proxyInitializeMethod, []byte(proxyTestInitialize))
	proxy.client = &http.Client{Transport: proxyTestTransport(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, sendErr := proxy.sendWithRetry(ctx, []byte(proxyTestToolCall(2, "ls", `{}`)), false)
		done <- sendErr
	}()
	testwait.Until(t, "the refused send to wait out its retry delay", func() (bool, string) {
		step, due := fake.NextDue()
		return due && step == proxyRetryDelay, fake.String()
	})
	fake.Advance(proxyRetryDelay)
	conn, _ := testwait.Receive(t, "the replay to probe the daemon's runtime", accepted)
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close probe connection: %v", err)
		}
	}()
	cancel()
	if sendErr, _ := testwait.Receive(
		t,
		"the cancelled request to leave the unresponsive daemon's runtime probe",
		done,
	); !errors.Is(
		sendErr,
		context.Canceled,
	) {
		t.Fatalf("sendWithRetry() error = %v, want context.Canceled", sendErr)
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
	// The start's probe deadline sits on a fake that never reaches it: only
	// the cancellation ends the probe.
	probeClock(t)
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
			_, _ = testwait.Receive(t, "the stdio start to end after closing the probe", done)
		}
	}()
	conn, _ = testwait.Receive(t, "the stdio start to probe the daemon", accepted)
	cancel()
	err, _ = testwait.Receive(t, "the cancelled stdio start to leave the daemon probe", done)
	finished = true
	if !errors.Is(err, context.Canceled) {
		t.Errorf("stdio start error = %v, want context.Canceled", err)
	}
	if strings.Contains(warnings.String(), "using in-process MCP") {
		t.Errorf("cancelled stdio start fell back: %s", warnings.String())
	}
}

func TestRunStdioCancellationAfterSuccessfulProbe(t *testing.T) {
	previous := stdioProbeDaemon
	t.Cleanup(func() { stdioProbeDaemon = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdioProbeDaemon = func(context.Context, string) (DaemonStatus, error) { cancel(); return DaemonStatus{PID: 1}, nil }
	var warnings proxyTestBuffer
	service := stdioTestService("cancel-after-probe", &warnings)
	professor := stdioTestProfessor(t, service)
	r, w := io.Pipe()
	defer func() {
		if closeErr := errors.Join(r.Close(), w.Close()); closeErr != nil {
			t.Errorf("close cancelled stdio pipes: %v", closeErr)
		}
	}()
	err := professor.runStdioTransport(ctx, io.NopCloser(strings.NewReader("")), w, stdioTestOptions(service, "unused"))
	if !errors.Is(err, context.Canceled) || err.Error() != "pfm mcp stdio: daemon probe cancelled: context canceled" {
		t.Fatalf("stdio error = %v; want actual context cancellation", err)
	}
}
