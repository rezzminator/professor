package inject

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// countingClock hands back a fresh, later instant on every Now() call while
// every other Clock method delegates to the embedded clock.Clock — a thin
// adapter (never a second fake) for the one test that needs "one second
// elapsed" per poll without a real sleep or a driven Advance.
type countingClock struct {
	clock.Clock
	start time.Time
	calls int
}

func (c *countingClock) Now() time.Time {
	c.calls++
	return c.start.Add(time.Duration(c.calls) * time.Second)
}

// fixedClock hands back the SAME instant from every Now() call while every
// other Clock method delegates to the embedded clock.Clock — the seam a
// test reaches for instead of clock.NewFake whenever the code path under
// test can still block on a real Sleep (acquireTargetLock's lock-settle
// wait, chiefly): a bare Fake never advances on its own, so a Sleep it was
// never told to release hangs the test forever, where clock.Real's Sleep
// just... sleeps, briefly, the way it always has.
type fixedClock struct {
	clock.Clock
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

// paneFrame is one observation of the pane plus the phase it belongs to, so a
// test can assert WHERE the waiter made its decision and not merely that it
// eventually returned.
type paneFrame struct {
	phase   string
	capture string
	// visible is what a capture of the VISIBLE fold alone returns, when that
	// differs from the history capture samplePane asks for. "" means the two
	// agree. A frame that sets it models the one shape the receipt count
	// exists for: a receipt that is in the pane's history but clipped out of
	// the visible fold, and scrolls back into view later.
	visible string
	// err is a capture that FAILED. The zero paneSample is not what this
	// frame hands back — the error is, so the waiter can tell a pane it could
	// not read from a pane with nothing on it.
	err error
}

const (
	phasePrimary    = "primary-turn"     // the typed primary is still running
	phaseGap        = "pre-compact-idle" // idle, compaction has NOT started
	phaseCompacting = "compacting"       // the compaction turn is running
	phaseDone       = "compaction-done"  // idle, compaction provably finished
	phaseResumed    = "resumed-work"     // the session resumed on its own
	phaseLate       = "long-after"       // idle again, far too late to be useful
)

// Real captures. busyPattern (guards.go:13) keys on "esc to interrupt"; the
// compaction receipt is what Claude Code prints once compaction has actually
// happened, which is the only positive evidence in the pane that the turn the
// waiter was told to ride out is the turn that ran.
const (
	captureBusy    = "working on it\n  esc to interrupt\n"
	captureIdle    = "conversation\n❯ "
	captureReceipt = "Compacted (ctrl+o to see full summary)\n❯ "
)

// paneScript replays a fixed sequence of frames and remembers the last one it
// handed out, so the waiter's decision point is observable.
type paneScript struct {
	*fakeTmux
	mu     sync.Mutex
	frames []paneFrame
	served int
}

func (script *paneScript) Capture(
	_ context.Context,
	_, _ string,
	_ bool,
	scrollback int,
) (string, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	index := script.served
	if index >= len(script.frames) {
		index = len(script.frames) - 1
	}
	script.served++
	frame := script.frames[index]
	if frame.err != nil {
		return "", frame.err
	}
	if scrollback == 0 && frame.visible != "" {
		return frame.visible, nil
	}
	return frame.capture, nil
}

// decidedIn names the phase the waiter was looking at when it stopped waiting.
func (script *paneScript) decidedIn() string {
	script.mu.Lock()
	defer script.mu.Unlock()
	index := script.served - 1
	if index < 0 {
		index = 0
	}
	if index >= len(script.frames) {
		index = len(script.frames) - 1
	}
	return script.frames[index].phase
}

func newScriptedEngine(t *testing.T, frames []paneFrame) (*Engine, *paneScript) {
	t.Helper()
	fake := &fakeTmux{capture: captureIdle}
	engine := newTestEngine(t, "cc-1-2-3", fake)
	script := &paneScript{fakeTmux: fake, frames: frames}
	engine.tmux = script
	// Every wait is unbounded in tries and free in wall-clock, so the test
	// measures the waiter's DECISION and never the machine it runs on.
	engine.options.ThenMin = time.Nanosecond
	engine.options.Poll = time.Nanosecond
	engine.options.ThenIdlePoll = time.Nanosecond
	engine.options.ThenSettle = time.Nanosecond
	engine.options.ThenBusyTries = 200
	engine.options.ThenIdleTries = 200
	engine.options.ThenIdleStable = 3
	return engine, script
}

// mustSettle runs the settled-turn wait and fails the test on the baseline
// error — the state that means the pane could not be read even once, which no
// scripted fixture here produces (settled_test.go covers that one on purpose).
func mustSettle(t *testing.T, engine *Engine) bool {
	t.Helper()
	observed, err := engine.waitForSettledTurn(context.Background(), "", "chat", pfmengine.Claude)
	if err != nil {
		t.Fatalf("waitForSettledTurn() baseline error: %v", err)
	}
	return observed
}

func repeatFrame(phase, capture string, count int) []paneFrame {
	frames := make([]paneFrame, 0, count)
	for range count {
		frames = append(frames, paneFrame{phase: phase, capture: capture})
	}
	return frames
}

// TestThenWaiterDoesNotDeliverBeforeTheCompactionRuns guards the chained hop.
// It starts after /compact was typed; an idle gap before that turn runs is not
// the boundary for the following steer.
func TestThenWaiterDoesNotDeliverBeforeTheCompactionRuns(t *testing.T) {
	var frames []paneFrame
	frames = append(frames, repeatFrame(phaseGap, captureIdle, 6)...)
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureReceipt, 6)...)

	engine, script := newScriptedEngine(t, frames)
	if observed := mustSettle(t, engine); !observed {
		t.Fatal("chained hop did not observe the compaction turn")
	}

	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: it delivered the steer "+
				"before the compaction ever ran, so the steer dies with the "+
				"pre-compaction session",
			got, phaseDone,
		)
	}
}

// TestThenWaiterDoesNotDeliverIntoResumedWork guards the chained hop against
// waiting past a brief receipt after compaction into work that resumed later.
func TestThenWaiterDoesNotDeliverIntoResumedWork(t *testing.T) {
	var frames []paneFrame
	frames = append(frames, paneFrame{phase: phaseGap, capture: captureIdle})
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, paneFrame{phase: phaseDone, capture: captureReceipt})
	frames = append(frames, repeatFrame(phaseResumed, captureBusy, 8)...)
	frames = append(frames, repeatFrame(phaseLate, captureReceipt, 6)...)

	engine, script := newScriptedEngine(t, frames)
	if observed := mustSettle(t, engine); !observed {
		t.Fatal("chained hop did not observe the compaction turn")
	}

	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: it slept through the one "+
				"idle that followed the compaction and delivered the steer "+
				"into work that had already resumed",
			got, phaseDone,
		)
	}
}

// TestThenWaiterStillReleasesWithoutACompactionReceipt guards the chained hop
// for a primary that prints no receipt, such as a reload or plain message. It
// must still ride out the turn after the idle gap.
func TestThenWaiterStillReleasesWithoutACompactionReceipt(t *testing.T) {
	// The pre-compaction gap is deliberately LONGER than the stability window,
	// so a waiter that counts steady idle before it has seen the primary's turn
	// begin releases here and the test catches it. The trailing frames are busy
	// for the same reason in the other direction: a waiter that bounds out
	// without ever deciding ends up in resumed work, not in a phase that would
	// have let it pass by accident.
	var frames []paneFrame
	frames = append(frames, repeatFrame(phaseGap, captureIdle, 5)...)
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureIdle, 3)...)
	frames = append(frames, repeatFrame(phaseResumed, captureBusy, 8)...)

	engine, script := newScriptedEngine(t, frames)
	if observed := mustSettle(t, engine); !observed {
		t.Fatal("chained hop did not observe the primary turn")
	}

	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: with no receipt to key on "+
				"the chained hop must still ride out the typed primary's turn",
			got, phaseDone,
		)
	}
}

// TestCompactionReceiptNeedsToAppear guards the chained hop against an old
// receipt already in its baseline. Only a receipt from this turn counts.
func TestCompactionReceiptNeedsToAppear(t *testing.T) {
	var frames []paneFrame
	// The pane already shows an older receipt when the waiter wakes up.
	frames = append(frames, repeatFrame(phaseGap, captureReceipt, 4)...)
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureReceipt, 6)...)

	engine, script := newScriptedEngine(t, frames)
	if !strings.Contains(frames[0].capture, "Compacted") {
		t.Fatalf("fixture no longer shows a stale receipt during the gap")
	}
	if observed := mustSettle(t, engine); !observed {
		t.Fatal("chained hop did not observe the current compaction turn")
	}

	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: it accepted a receipt "+
				"left over from an earlier compaction as proof that this "+
				"compaction had run",
			got, phaseDone,
		)
	}
}

// TestThenWaiterDoesNotBurnTheBudgetWaitingForATurnThatAlreadyRan guards the
// bound on the new "wait for the primary's turn to start" step. If the turn
// began and ended inside ThenMin — or the pane simply never reports busy — that
// step has nothing left to observe, and waiting out the full idle budget would
// hold the steer for minutes and strand the chain. It must give up quickly,
// deliver anyway, and say the boundary was never observed rather than let a
// guess pass for proof.
func TestThenWaiterDoesNotBurnTheBudgetWaitingForATurnThatAlreadyRan(t *testing.T) {
	frames := repeatFrame(phaseDone, captureIdle, 4)

	engine, script := newScriptedEngine(t, frames)
	engine.options.ThenBusyTries = 3
	engine.options.ThenIdleTries = 500
	engine.options.ThenIdleStable = 2

	observed := mustSettle(t, engine)

	if observed {
		t.Fatal(
			"waiter claimed it observed a turn boundary when the pane never " +
				"went busy — that is a guess reported as proof",
		)
	}
	const budget = 20
	if script.served > budget {
		t.Fatalf(
			"waiter sampled the pane %d times before giving up, want at most "+
				"%d: it burned the idle budget waiting for a turn that was "+
				"never going to start, stranding the steer",
			script.served, budget,
		)
	}
}

// TestThenWaiterAcceptsThePrimaryTurnAlreadyBusy pins the chained hop's
// first sample: a busy pane can already be running the primary turn, and the
// waiter must release when it ends rather than wait for another turn.
func TestThenWaiterAcceptsThePrimaryTurnAlreadyBusy(t *testing.T) {
	// The pane is already busy with the primary's own turn on the first sample.
	var frames []paneFrame
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureIdle, 6)...)
	frames = append(frames, repeatFrame(phaseLate, captureBusy, 6)...)

	engine, script := newScriptedEngine(t, frames)
	observed := mustSettle(t, engine)

	if !observed {
		t.Fatal(
			"waiter reported no turn boundary for a chained hop whose " +
				"turn it watched start and finish — a false warning on the " +
				"chained --then hop",
		)
	}
	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: it waited for a second turn "+
				"that was never going to come",
			got, phaseDone,
		)
	}
}

// TestDeliverThenHoldsForTypistThenDelivers pins the waiter half of the
// typist guard (Task A.4): waitForQuietTypist holds DeliverThen back while a
// human keeps typing and delivers exactly once quiet holds for TypistQuiet.
// The fake models a typist whose LAST keystroke never moves (a human who
// typed once and stopped) while the clock advances one second per poll, so
// "quiet" here is purely a function of elapsed time crossing TypistQuiet —
// exactly what the production code computes. Revert waitForQuietTypist's
// call in DeliverThen (comment it out) and this fails too: engine.inject's
// OWN typist guard (Task A.3) still catches the still-typing pane on the
// delivery attempt that follows, but with its OWN message text ("a human is
// typing in..."), not this test's "delivers once quiet holds" shape — proof
// the waiter-level check is a distinct, load-bearing layer and not just a
// restatement of the delivery-time guard.
func TestDeliverThenHoldsForTypistThenDelivers(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true, clientAttached: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-typist-hold", fake, spawner)
	engine.options.ThenMin = time.Nanosecond
	engine.options.ThenBusyTries = 1
	engine.options.ThenIdlePoll = time.Nanosecond
	engine.options.ThenIdleStable = 1
	engine.options.ThenSettle = time.Nanosecond
	engine.options.ThenIdleTries = 10
	engine.options.TypistQuiet = 3 * time.Second

	start := time.Unix(1_700_000_000, 0)
	fake.clientActivity = start
	counting := &countingClock{Clock: clock.Real, start: start}
	engine.options.Clock = counting

	result, err := engine.DeliverThen(context.Background(), ThenWait{Target: "chat", Steers: []string{"resume"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || !result.Typed {
		t.Fatalf("DeliverThen() = %+v, want a confirmed delivery once the typist went quiet", result)
	}
	if counting.calls < 3 {
		t.Fatalf(
			"delivered before the typist actually went quiet: waitForQuietTypist's clock only advanced %d time(s), want at least 3 (TypistQuiet=3s at 1s/poll)",
			counting.calls,
		)
	}
	enters := 0
	for _, key := range fake.keys {
		if key == "Enter" {
			enters++
		}
	}
	if enters != 1 {
		t.Fatalf("keys=%q, want exactly one Enter once the typist cleared", fake.keys)
	}
}

// TestDeliverThenRefusesWhenTypistNeverClears pins the exhaustion half: a
// typist whose last keystroke NEVER ages past TypistQuiet within
// ThenIdleTries polls gets a refused chain, code 7, "then steer NOT
// delivered" in the message, and nothing typed. Revert
// waitForQuietTypist's call in DeliverThen and this fails: the chain
// delivers straight over the typing human instead of refusing.
func TestDeliverThenRefusesWhenTypistNeverClears(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true, clientAttached: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-typist-refuse", fake, spawner)
	engine.options.ThenMin = time.Nanosecond
	engine.options.ThenBusyTries = 1
	engine.options.ThenIdlePoll = time.Nanosecond
	engine.options.ThenIdleStable = 1
	engine.options.ThenSettle = time.Nanosecond
	engine.options.ThenIdleTries = 2
	engine.options.TypistQuiet = 3 * time.Second

	start := time.Unix(1_700_000_000, 0)
	fake.clientActivity = start
	// The clock always reads "1s after the last keystroke" — quiet never
	// crosses the 3s TypistQuiet threshold no matter how many times it is
	// sampled.
	engine.options.Clock = fixedClock{Clock: clock.Real, now: start.Add(time.Second)}

	result, err := engine.DeliverThen(context.Background(), ThenWait{Target: "chat", Steers: []string{"resume"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != CodeBusy || result.Status != "typing" {
		t.Fatalf("DeliverThen() = %+v, want a refused (code 7, typing) chain", result)
	}
	if !strings.Contains(result.Message, "then steer NOT delivered") {
		t.Fatalf("refusal %q lacks \"then steer NOT delivered\"", result.Message)
	}
	if len(fake.keys) != 0 || len(fake.literals) != 0 {
		t.Fatalf("typed despite the typist never clearing: keys=%q literals=%q", fake.keys, fake.literals)
	}
}

// TestDeliverThenReportsUndeliveredWhenTmuxUnreadable pins the tri-state half
// of the typist guard (F1 of the merge-gating review): when ClientActivity
// errors on EVERY poll across the whole ThenIdleTries wait window — a dead
// socket, a pane that vanished mid-wait, anything that keeps failing to
// answer "who's there" — waitForQuietTypist must not let that alias with "a
// human kept typing." A tmux read failure is a failure to look, never
// evidence of a typist, so DeliverThen must report Code 6 / status
// "undelivered" naming the tmux error, not Code 7 / status "typing" naming a
// human that was never actually observed.
func TestDeliverThenReportsUndeliveredWhenTmuxUnreadable(t *testing.T) {
	readErr := errors.New("boom: no server running on socket")
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true, clientErr: readErr}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-typist-unreadable", fake, spawner)
	engine.options.ThenMin = time.Nanosecond
	engine.options.ThenBusyTries = 1
	engine.options.ThenIdlePoll = time.Nanosecond
	engine.options.ThenIdleStable = 1
	engine.options.ThenSettle = time.Nanosecond
	engine.options.ThenIdleTries = 3
	engine.options.TypistQuiet = 3 * time.Second

	start := time.Unix(1_700_000_000, 0)
	engine.options.Clock = fixedClock{Clock: clock.Real, now: start}

	result, err := engine.DeliverThen(context.Background(), ThenWait{Target: "chat", Steers: []string{"resume"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != CodeUndelivered || result.Status != "undelivered" {
		t.Fatalf(
			"DeliverThen() = %+v, want a Code 6 undelivered result when tmux could not be read for the whole wait window (not Code 7 \"typing\" — an error is never evidence of a typist)",
			result,
		)
	}
	if !strings.Contains(result.Message, "then steer NOT delivered") ||
		!strings.Contains(result.Message, "could not read who is at") ||
		!strings.Contains(result.Message, readErr.Error()) {
		t.Fatalf(
			"undelivered message %q lacks the \"could not read\" shape naming the tmux error %v",
			result.Message,
			readErr,
		)
	}
	if strings.Contains(result.Message, "a human kept typing") {
		t.Fatalf(
			"undelivered message %q falsely renders a tmux read failure as \"a human kept typing\"",
			result.Message,
		)
	}
	if len(fake.keys) != 0 || len(fake.literals) != 0 {
		t.Fatalf(
			"typed despite tmux being unreadable the whole wait window: keys=%q literals=%q",
			fake.keys,
			fake.literals,
		)
	}
}

// TestDeliverThenReturnsPromptlyWhenCtxIsCancelledMidWait (L1-T3): no
// existing test ever cancels the ctx DeliverThen is given, so a regression
// that silently swapped it for context.Background() anywhere along the wait
// chain (waitForSettledTurn's Sleep/Capture calls) would still pass every
// other test in this package. Each configured wait step here is 2 seconds
// (6 poll/min/settle steps deep); a ctx that is genuinely threaded through
// returns almost instantly once cancelled — a wait that dropped it would
// still be sleeping when the bounded select below times out.
func TestDeliverThenReturnsPromptlyWhenCtxIsCancelledMidWait(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle}
	engine := newTestEngine(t, "cc-then-cancel", fake)
	// Every capture answers the way a real tmux exec does once its ctx is
	// already done: a failure, not a reading — see settled_test.go's own
	// baseline-retry fixture for the sibling shape.
	script := &paneScript{
		fakeTmux: fake,
		frames:   []paneFrame{{phase: phasePrimary, err: context.Canceled}},
	}
	engine.tmux = script
	engine.options.ThenMin = 2 * time.Second
	engine.options.ThenIdlePoll = 2 * time.Second
	engine.options.ThenSettle = 2 * time.Second
	engine.options.ThenBusyTries = 3
	engine.options.ThenIdleTries = 3
	engine.options.ThenIdleStable = 1

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := engine.DeliverThen(ctx, ThenWait{
			SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-then-cancel"),
			Target:     "%1",
			Steers:     []string{"resume the wave"},
		})
		done <- outcome{result, err}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf(
				"DeliverThen() returned a Go error %v, want the cancellation named on the Result instead",
				out.err,
			)
		}
		if !strings.Contains(out.result.Message, context.Canceled.Error()) {
			t.Fatalf("Message = %q, want it to name the cancelled context", out.result.Message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal(
			"DeliverThen did not return promptly after its ctx was cancelled — " +
				"each configured wait step is 2s across a 6-step budget, so an " +
				"honoured cancellation must land well inside this 3s bound",
		)
	}
}

// captureBusyReceipt is the pane a background sub-agent leaves behind: the
// compaction receipt is on screen, the main turn is over, and the agent's
// own footer keeps busyPattern matching for as long as it runs.
const captureBusyReceipt = "Compacted (ctrl+o to see full summary)\n" +
	"● Background agent running…\n  esc to interrupt\n❯ "

// TestThenWaiterAcceptsTheReceiptWhileABackgroundAgentKeepsThePaneBusy is
// Wave 8 item 5 (beat E1.20): a chat with a background sub-agent never reads
// idle, so a waiter that insists on !busy before it trusts the receipt sits
// out its whole budget and then delivers with the "no turn boundary" WARNING
// — ten minutes late and unproven. The receipt is the boundary: a receipt
// that was NOT on screen when the waiter woke and IS on screen now proves
// this turn's compaction ran, whatever the footer says.
func TestThenWaiterAcceptsTheReceiptWhileABackgroundAgentKeepsThePaneBusy(t *testing.T) {
	var frames []paneFrame
	frames = append(frames, repeatFrame(phasePrimary, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureBusyReceipt, 6)...)

	engine, script := newScriptedEngine(t, frames)
	engine.options.ThenBusyTries = 4
	engine.options.ThenIdleTries = 8
	observed := mustSettle(t, engine)

	if !observed {
		t.Fatal(
			"waiter reported no turn boundary although this turn's own receipt " +
				"appeared while it watched — it waited for an idle a background " +
				"agent never grants and will deliver late WITH the WARNING",
		)
	}
	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf("waiter released in phase %q, want %q", got, phaseDone)
	}
	// The baseline capture takes the first primary frame, the loop the
	// other two, and the FIRST receipt frame decides: any later release
	// rode the busy footer instead.
	if script.served != 4 {
		t.Fatalf("waiter sampled %d times, want 4 (baseline + 2 busy + the receipt frame)", script.served)
	}
}

// TestThenWaiterIgnoresAReceiptThatWasAlreadyOnScreenWhenItWoke is the
// stale-receipt trap of the rule above (the class reload_then_proof_test.go's
// stale placeholder pins): a receipt already in the baseline capture and
// never re-printed proves nothing about THIS turn, footer or no footer.
//
// The fixture is deliberately one the OLD condition passes and the new one
// fails (F7): the baseline carries a receipt AND a busy footer, a turn is
// then seen to start, and the pane goes idle with that same receipt still on
// it. "turnStarted && receipt && !busy" fires on the first idle frame — it
// never asked whether the receipt was NEW — while the count-based rule sees
// one receipt before and one after and refuses. The idle-stable fallback is
// held out of reach (IdleStable above the whole budget) so the two codepaths
// differ in the RESULT, not merely in when they returned.
func TestThenWaiterIgnoresAReceiptThatWasAlreadyOnScreenWhenItWoke(t *testing.T) {
	var frames []paneFrame
	frames = append(frames, repeatFrame(phasePrimary, captureBusyReceipt, 3)...)
	frames = append(frames, repeatFrame(phaseLate, captureReceipt, 12)...)

	engine, script := newScriptedEngine(t, frames)
	engine.options.ThenBusyTries = 2
	engine.options.ThenIdleTries = 6
	engine.options.ThenIdleStable = 30
	observed := mustSettle(t, engine)

	if observed {
		t.Fatal(
			"waiter took a receipt that was already on screen in its baseline " +
				"capture as proof this turn's compaction ran — a receipt must be " +
				"newly PRINTED, not merely still there once the footer cleared",
		)
	}
	if script.served < 3 {
		t.Fatalf("waiter gave up after %d samples without exhausting its budget", script.served)
	}
}
