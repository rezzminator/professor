package hookentry

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/inject"
)

// blockingWaiter never returns until released — the shape of the real waiter
// for the whole turn it rides out — and records what it was handed.
type blockingWaiter struct {
	release chan struct{}
	got     inject.ThenWait
	result  inject.Result
}

func (waiter *blockingWaiter) DeliverThen(_ context.Context, wait inject.ThenWait) (inject.Result, error) {
	waiter.got = wait
	<-waiter.release
	return waiter.result, nil
}

// lockedBuffer is a bytes.Buffer the test can read while Then writes it from
// another goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *lockedBuffer) Write(raw []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(raw)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func swapThenWaiter(t *testing.T, waiter thenWaiter) {
	t.Helper()
	original := newThenWaiter
	t.Cleanup(func() { newThenWaiter = original })
	newThenWaiter = func(*config.Runtime) (thenWaiter, error) { return waiter, nil }
}

// TestThenLogsItsContractBeforeWaiting is Wave 8 item 5's first log line:
// until now the ONLY line the waiter ever wrote was its verdict at the END,
// so a log read mid-wait was empty — indistinguishable from a waiter that
// never started. The start line goes down BEFORE DeliverThen blocks and says,
// in the waiter's own words, what it is waiting for.
func TestThenLogsItsContractBeforeWaiting(t *testing.T) {
	waiter := &blockingWaiter{release: make(chan struct{}), result: inject.Result{Code: 0, Message: "delivered"}}
	swapThenWaiter(t, waiter)
	log := &lockedBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- Then([]string{
			"--socket", "/tmp/tmux-jail/cc-1-2-3",
			"--target", "%0",
			"--self",
			"--steer", "resume the wave",
			"--steer", "then report",
		}, log)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for log.String() == "" {
		if time.Now().After(deadline) {
			t.Fatal("the waiter log stayed EMPTY while DeliverThen blocked — nothing was written before the wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case code := <-done:
		t.Fatalf("Then returned %d before the waiter was released — the fake did not block", code)
	default:
	}
	want := "then waiter: start — target %0 on /tmp/tmux-jail/cc-1-2-3 · self=true · 2 steer(s) · waiting for: " +
		inject.WaitingFor(true, "") + "\n"
	if got := log.String(); got != want {
		t.Fatalf("first log line = %q, want %q", got, want)
	}

	close(waiter.release)
	if code := <-done; code != 0 {
		t.Fatalf("Then = %d after the waiter delivered, want 0", code)
	}
	if !strings.Contains(log.String(), "then steer -> %0 (code 0): delivered") {
		t.Fatalf("the verdict line is missing after the start line: %q", log.String())
	}
}

// TestThenHandsTheEngineAndTheContractToTheWaiter: the spawner states the
// target's engine as `--engine`; the waiter must pass it through, because
// DeliverThen refuses the steady-idle guess on a Codex pane by that field —
// and the start line names the engine so the log says which contract held.
func TestThenHandsTheEngineAndTheContractToTheWaiter(t *testing.T) {
	waiter := &blockingWaiter{release: make(chan struct{}), result: inject.Result{Code: inject.CodeUndelivered}}
	close(waiter.release)
	swapThenWaiter(t, waiter)
	var log bytes.Buffer

	code := Then([]string{
		"--socket", "/tmp/tmux-jail/cx-1-2-3",
		"--target", "%0",
		"--engine", string(pfmengine.Codex),
		"--steer", "resume the wave",
	}, &log)

	if code != inject.CodeUndelivered {
		t.Fatalf("Then = %d, want the waiter's own code %d", code, inject.CodeUndelivered)
	}
	want := inject.ThenWait{
		SocketPath: "/tmp/tmux-jail/cx-1-2-3",
		Target:     "%0",
		Steers:     []string{"resume the wave"},
		Engine:     string(pfmengine.Codex),
	}
	if waiter.got.SocketPath != want.SocketPath || waiter.got.Target != want.Target ||
		waiter.got.Engine != want.Engine || waiter.got.SelfTarget ||
		len(waiter.got.Steers) != 1 || waiter.got.Steers[0] != want.Steers[0] {
		t.Fatalf("waiter was handed %+v, want %+v", waiter.got, want)
	}
	if !strings.Contains(log.String(), "engine codex") {
		t.Fatalf("start line does not name the Codex contract: %q", log.String())
	}
}
