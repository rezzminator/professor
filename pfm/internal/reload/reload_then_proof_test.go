package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// driveFakeClock runs fn to completion, advancing clk whenever it has a
// pending sleep, so a loop of real sleeps inside fn resolves in however
// long the scheduler takes to hand control back — never the real duration
// each sleep names. It fails the test if fn does not finish inside a
// generous real wall-clock bound (a genuine hang, never the fake clock
// running slow).
func driveFakeClock(t *testing.T, clk *clock.Fake, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-done:
			return
		default:
		}
		if clk.Pending() > 0 {
			clk.Advance(time.Hour)
		} else {
			runtime.Gosched()
		}
		if time.Now().After(deadline) {
			t.Fatal(
				"driveFakeClock: fn did not finish before the real wall-clock bound — a genuine hang, not the fake clock",
			)
		}
	}
}

// This file pins deliverThen's paste-placeholder proof (issue #12): a --then
// prompt must be proven delivered from the ACTIVE composer only, a stale
// placeholder left over in scrollback must never stand in for that proof, a
// failed baseline capture must narrow the proof back to the tail needle, and
// an unconfirmed submit must surface as an error the caller can recover
// from instead of a silent false "success".

// --- (a) a collapsed paste in the COMPOSER proves delivery ----------------

// composerPastePlaceholderTmux simulates a --then prompt long enough that
// Claude Code collapses it into "[Pasted text #N +M lines]" in the LIVE
// composer — the prompt's own tail characters never render literally, so
// only the placeholder can prove the send landed.
type composerPastePlaceholderTmux struct {
	fakeReloadTmux
	enterCount int
	submitted  bool
}

func (tmux *composerPastePlaceholderTmux) Capture(context.Context, string, string) (string, error) {
	if tmux.literal == "" {
		// Pre-send: both the input-box wait and the pre-send baseline see an
		// empty composer.
		return "Chat\n❯ ", nil
	}
	if tmux.submitted {
		return "Chat\n❯ ", nil
	}
	return "Chat\n❯ [Pasted text #3 +72 lines]", nil
}

func (tmux *composerPastePlaceholderTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	if key == "Enter" {
		tmux.enterCount++
		if tmux.enterCount >= 2 {
			tmux.submitted = true
		}
	}
	return tmux.fakeReloadTmux.SendKey(ctx, socket, pane, key)
}

func TestDeliverThenAcceptsAComposerPastePlaceholderAsProofOfDelivery(t *testing.T) {
	t.Parallel()
	tmux := &composerPastePlaceholderTmux{}
	proc := fakeReloadProc{
		pids: []int{801},
		argv: map[int][]string{801: {"claude"}},
		stat: map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	then := strings.Repeat("a very large pasted paragraph Claude Code collapses in the composer. ", 20)
	err := deliverThen(
		context.Background(),
		Request{
			Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-paste-then", Pane: "%7",
			PanePID: 700, Then: then,
		},
		Options{ThenTries: 2},
		tmux,
		proc,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("deliverThen() error = %v, want a composer placeholder to prove delivery", err)
	}
	if tmux.enterCount < 2 {
		t.Fatalf("enterCount = %d, want the submit sequence to run once delivery was proven", tmux.enterCount)
	}
}

// --- (b) a STALE placeholder in scrollback must NOT prove delivery --------

// stalePlaceholderScrollbackTmux simulates a respawned --resume pane whose
// SCROLLBACK still renders a placeholder pasted in an EARLIER turn, while
// the live composer stays empty for the current --then send. Every capture
// also changes (an incrementing "spinner" line), the way a real TUI
// animates every frame, so "capture != baseline" is trivially true here and
// must never stand in as delivery proof on its own.
type stalePlaceholderScrollbackTmux struct {
	fakeReloadTmux
	tick       int
	enterCount int
}

func (tmux *stalePlaceholderScrollbackTmux) Capture(context.Context, string, string) (string, error) {
	tmux.tick++
	return fmt.Sprintf("Chat\n[Pasted text #1 +5 lines]  (an earlier turn)\nspinner %d\n❯ ", tmux.tick), nil
}

func (tmux *stalePlaceholderScrollbackTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	if key == "Enter" {
		tmux.enterCount++
	}
	return tmux.fakeReloadTmux.SendKey(ctx, socket, pane, key)
}

func TestDeliverThenRefusesAStalePlaceholderLeftInScrollback(t *testing.T) {
	t.Parallel()
	tmux := &stalePlaceholderScrollbackTmux{}
	proc := fakeReloadProc{
		pids: []int{801},
		argv: map[int][]string{801: {"claude"}},
		stat: map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	fakeClk := clock.NewFake(time.Unix(0, 0))
	var err error
	driveFakeClock(t, fakeClk, func() {
		err = deliverThen(
			context.Background(),
			Request{
				Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-stale-scrollback", Pane: "%7",
				PanePID: 700, Then: "continue the task",
			},
			Options{ThenTries: 2, Clock: fakeClk},
			tmux,
			proc,
			io.Discard,
		)
	})
	if err == nil {
		t.Fatal("deliverThen() = nil, want refusal — the placeholder is stale scrollback, not this send's composer")
	}
	if !strings.Contains(err.Error(), "never rendered") {
		t.Fatalf("error = %q, want the blind-Enter refusal message", err)
	}
	if tmux.enterCount != 0 {
		t.Fatalf("enterCount = %d, want the submit sequence to never run for an unproven send", tmux.enterCount)
	}
}

// --- (c) a failed baseline capture downgrades to tail-only proof ----------

// baselineFailureTmux fails exactly the pre-send baseline capture (the 2nd
// Capture call), then renders a composer that is either a bare paste
// placeholder or the prompt's own tail text, depending on composerMode.
type baselineFailureTmux struct {
	fakeReloadTmux
	captures     int
	composerMode string // "placeholder" or "tail"
	then         string
	enterCount   int
	submitted    bool
}

func (tmux *baselineFailureTmux) Capture(context.Context, string, string) (string, error) {
	tmux.captures++
	switch tmux.captures {
	case 1:
		// The input-box wait: Claude is already sitting at its prompt.
		return "Chat\n❯ ", nil
	case 2:
		// The pre-send baseline capture — simulate a transient tmux failure.
		return "", errors.New("capture-pane: pane not found (transient)")
	default:
		if tmux.composerMode != "tail" {
			return "Chat\n❯ [Pasted text #4 +12 lines]", nil
		}
		if tmux.submitted {
			return "Chat\n❯ ", nil
		}
		return "Chat\n❯ " + tmux.then, nil
	}
}

func (tmux *baselineFailureTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	if key == "Enter" {
		tmux.enterCount++
		if tmux.enterCount >= 2 {
			tmux.submitted = true
		}
	}
	return tmux.fakeReloadTmux.SendKey(ctx, socket, pane, key)
}

func TestDeliverThenRequiresTheTailNeedleWhenTheBaselineCaptureFailed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		composerMode string
		wantAccepted bool
	}{
		{"a bare placeholder is refused without a baseline", "placeholder", false},
		{"the prompt's own tail text is still accepted", "tail", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			tmux := &baselineFailureTmux{composerMode: testCase.composerMode, then: "continue the task"}
			proc := fakeReloadProc{
				pids: []int{801},
				argv: map[int][]string{801: {"claude"}},
				stat: map[int]gather.ProcStat{801: {ParentPID: 700}},
			}
			fakeClk := clock.NewFake(time.Unix(0, 0))
			var err error
			driveFakeClock(t, fakeClk, func() {
				err = deliverThen(
					context.Background(),
					Request{
						Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-baseline-fail", Pane: "%7",
						PanePID: 700, Then: tmux.then,
					},
					Options{ThenTries: 2, Clock: fakeClk},
					tmux,
					proc,
					io.Discard,
				)
			})
			if testCase.wantAccepted {
				if err != nil {
					t.Fatalf("deliverThen() error = %v, want the tail needle accepted despite the failed baseline", err)
				}
				return
			}
			if err == nil {
				t.Fatal("deliverThen() = nil, want a bare placeholder refused when the baseline capture failed")
			}
			if tmux.enterCount != 0 {
				t.Fatalf("enterCount = %d, want no submit attempt for an unproven send", tmux.enterCount)
			}
		})
	}
}

// --- (d) an unconfirmed submit returns an error, and the prompt survives --

// stuckSubmitTmux completes /exit, respawn, and the typed-proof phase
// normally, but the composer never clears no matter how many times Enter is
// sent — simulating a submit that never confirms.
type stuckSubmitTmux struct {
	fakeReloadTmux
}

func (tmux *stuckSubmitTmux) Capture(context.Context, string, string) (string, error) {
	if tmux.respawn == "" {
		if tmux.literal == "/exit" {
			return "Chat\n❯ /exit", nil
		}
		return "Chat\n❯ ", nil
	}
	if tmux.literal == "" {
		return "Chat\n❯ ", nil
	}
	// The prompt is typed but the composer NEVER clears, no matter how many
	// Enters land — the submit never confirms.
	return "Chat\n❯ " + tmux.literal, nil
}

func (tmux *stuckSubmitTmux) Respawn(_ context.Context, _, _, _, command string) error {
	tmux.respawn = command
	tmux.dead = false
	tmux.literal = ""
	return nil
}

func TestRunReturnsAnErrorAndSavesTheSentinelWhenSubmitIsNeverConfirmed(t *testing.T) {
	t.Parallel()
	tmux := &stuckSubmitTmux{}
	proc := fakeReloadProc{
		pids: []int{801},
		argv: map[int][]string{801: {"claude"}},
		stat: map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	sidDir := t.TempDir()
	socket := "/tmp/tmux-1000/probe-stuck-submit"
	fakeClk := clock.NewFake(time.Unix(0, 0))
	var err error
	driveFakeClock(t, fakeClk, func() {
		_, err = Run(
			context.Background(),
			Request{
				Engine:     pfmengine.Claude,
				SocketPath: socket,
				Pane:       "%7",
				PanePID:    700,
				CWD:        "/jail/project",
				Account:    1,
				AccountIDs: []int{1},
				Then:       "continue the task",
			},
			Options{SIDDir: sidDir, Delay: -1, Poll: -1, ExitTries: 2, ThenTries: 2, Clock: fakeClk},
			tmux,
			proc,
			io.Discard,
		)
	})
	if err == nil {
		t.Fatal("Run() = nil, want an error when the submit never confirms")
	}
	if !strings.Contains(err.Error(), "submission is unproven") {
		t.Fatalf("error = %q, want deliverThen's unconfirmed-submit reason to surface", err)
	}
	sentinel := filepath.Join(sidDir, filepath.Base(socket)+".then-failed")
	content, readErr := os.ReadFile(sentinel)
	if readErr != nil {
		t.Fatalf("read sentinel %q: %v — failThen must run when the error propagates", sentinel, readErr)
	}
	if string(content) != "continue the task\n" {
		t.Fatalf("sentinel content = %q, want the unsubmitted prompt preserved", content)
	}
}
