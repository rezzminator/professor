package inject

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stealingTmux embeds *fakeTmux and steals the delivery's lock — overwrites
// its owner file with a foreign pid — the first time SendLiteral runs,
// simulating another process stealing it mid-delivery (F9). A standalone
// wrapper here so the shared fakeTmux in engine_test.go never grows for one
// fixture's own needs.
type stealingTmux struct {
	*fakeTmux
	lockRoot, lockKey string
}

func (fake stealingTmux) SendLiteral(ctx context.Context, socketPath, target, text string) error {
	lockPath := filepath.Join(fake.lockRoot, lockDirName(fake.lockKey))
	_ = os.WriteFile(filepath.Join(lockPath, "owner"), []byte("999999 1\n"), 0o600)
	return fake.fakeTmux.SendLiteral(ctx, socketPath, target, text)
}

// TestDeliverStopsWhenTheLockIsStolenMidDelivery (L1-F9): a delivery whose
// lock is stolen mid-flight — the owner file now names another pid — must
// stop rather than keep heartbeating (and eventually pressing Enter on) a
// lock it no longer holds; it reports the loss under its own code instead of
// the generic CodeUndelivered.
func TestDeliverStopsWhenTheLockIsStolenMidDelivery(t *testing.T) {
	inner := &fakeTmux{capture: "› "}
	engine := newTestEngine(t, "cc-lock-steal", inner)
	engine.tmux = stealingTmux{
		fakeTmux: inner,
		lockRoot: engine.options.LockRoot,
		lockKey: filepath.Join(
			string(filepath.Separator), "tmp", "tmux-jail", "cc-lock-steal",
		) + ":%1",
	}

	result, err := engine.Inject(context.Background(), Request{Target: "beta", Message: "hello"})
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if result.Code != CodeLockLost {
		t.Fatalf("Code = %d, want CodeLockLost (%d): %+v", result.Code, CodeLockLost, result)
	}
	if result.Status != "lock_lost" {
		t.Fatalf("Status = %q, want lock_lost: %+v", result.Status, result)
	}
	for _, key := range inner.keys {
		if key == "Enter" {
			t.Fatalf("keys sent = %v — a delivery that lost its lock must never press Enter", inner.keys)
		}
	}
}

// scriptedCaptureTmux embeds *fakeTmux and always answers Capture with a
// fixed error — a standalone wrapper so these fixtures never grow the shared
// fakeTmux struct in engine_test.go.
type scriptedCaptureTmux struct {
	*fakeTmux
	err error
}

func (fake scriptedCaptureTmux) Capture(context.Context, string, string, bool, int) (string, error) {
	return "", fake.err
}

// TestEngineCaptureNamesACaptureFailureDistinctFromADeadPane (Lane-2 §5): a
// tmux capture that could not even RUN (the binary itself unreachable) must
// not report the same CodeDead a pane that genuinely answered gone gets —
// losing that cause is exactly "a probe that could not run rendering as
// absence". Engine.Capture's own err return stays nil either way, so every
// existing caller (mcpserv's chat_keys/chat_capture among them) keeps
// compiling and behaving exactly as before; the cause now rides in detail
// under the new inject.CodeCaptureFailed.
func TestEngineCaptureNamesACaptureFailureDistinctFromADeadPane(t *testing.T) {
	engine := newTestEngine(t, "cc-capture-fail", &fakeTmux{})
	engine.tmux = scriptedCaptureTmux{
		fakeTmux: &fakeTmux{},
		err:      &exec.Error{Name: "tmux", Err: exec.ErrNotFound},
	}
	_, _, code, detail, err := engine.Capture(context.Background(), "beta", 0)
	if err != nil {
		t.Fatalf("Capture() error = %v, want nil — every existing caller reads the failure off code/detail", err)
	}
	if code != CodeCaptureFailed {
		t.Fatalf("code = %d, want CodeCaptureFailed (%d)", code, CodeCaptureFailed)
	}
	if !strings.Contains(detail, "could not run tmux") || !strings.Contains(detail, exec.ErrNotFound.Error()) {
		t.Fatalf("detail = %q, want it to name the capture failure and its cause", detail)
	}
}

// TestEngineCaptureStillReportsAnOrdinaryDeadPaneAsCodeDead pins the
// unchanged half: tmux running and answering "no such pane" is genuine
// absence, still CodeDead, still the fixed "target pane is dead or
// unreadable" detail every existing caller already matches on.
func TestEngineCaptureStillReportsAnOrdinaryDeadPaneAsCodeDead(t *testing.T) {
	engine := newTestEngine(t, "cc-capture-dead", &fakeTmux{})
	engine.tmux = scriptedCaptureTmux{
		fakeTmux: &fakeTmux{},
		err:      errors.New("can't find pane: %9"),
	}
	_, _, code, detail, err := engine.Capture(context.Background(), "beta", 0)
	if err != nil {
		t.Fatalf("Capture() error = %v, want nil", err)
	}
	if code != CodeDead {
		t.Fatalf("code = %d, want CodeDead (%d) for an ordinary tmux answer", code, CodeDead)
	}
	if detail != "target pane is dead or unreadable" {
		t.Fatalf("detail = %q, want the unchanged dead-pane message", detail)
	}
}
