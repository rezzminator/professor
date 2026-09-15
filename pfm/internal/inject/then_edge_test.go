package inject

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/resolve"
)

// fakeSelf answers "who am I" without asking the machine. Resolving the target
// "self" for real needs a live tmux seat, so a test that leans on the ambient
// environment passes on a developer box that happens to be running inside a
// chat and fails in the container — which is exactly what this one did before
// the fence caught it. Supplying the identity makes the test measure the code.
type fakeSelf struct {
	identity resolve.Identity
}

func (fake fakeSelf) Identify(context.Context) (resolve.Identity, error) {
	return fake.identity, nil
}

// The two repros below pin the SAME defect from its two opposite ends.
//
// waitForSettledTurn rides out "the turn the primary started" using nothing but
// a busy bit. That bit has no identity: it reads true while the compaction runs
// AND while the caller's own turn runs AND while the post-compaction session
// resumes on its own. So the waiter latches onto whichever turn happened to be
// busy when it woke up, and then races whichever idle arrives first.
//
// Which way it loses depends on timing alone:
//
//   - the caller stops promptly -> the waiter latches the caller's turn, sees
//     the idle BEFORE compaction even starts, and delivers the steer EARLY,
//     into a pane that is about to be compacted out from under it.
//   - the caller keeps working -> the brief idle that follows the compaction is
//     shorter than the stability window, so the waiter misses its one true window
//     and delivers LATE, into work that already resumed.
//
// One bug, two faces. A fix that only moves the delivery point earlier or later
// trades one face for the other; the waiter has to identify the turn instead of
// counting on a coincidence.

// paneFrame is one observation of the pane plus the phase it belongs to, so a
// test can assert WHERE the waiter made its decision and not merely that it
// eventually returned.
type paneFrame struct {
	phase   string
	capture string
}

const (
	phaseCaller     = "caller-turn"      // the caller's own turn, still running
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
	_ int,
) (string, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	index := script.served
	if index >= len(script.frames) {
		index = len(script.frames) - 1
	}
	script.served++
	return script.frames[index].capture, nil
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

func repeatFrame(phase, capture string, count int) []paneFrame {
	frames := make([]paneFrame, 0, count)
	for range count {
		frames = append(frames, paneFrame{phase: phase, capture: capture})
	}
	return frames
}

// TestThenWaiterDoesNotDeliverBeforeTheCompactionRuns is the EARLY-landing
// repro. The caller ends its turn promptly, so the pane goes idle while the
// queued /compact is still waiting its turn. A waiter that only knows "was
// busy, now idle" reads that gap as the compaction having finished and fires
// the steer into a pane that is about to be compacted — the steer is consumed
// by the pre-compaction session and vanishes with it.
func TestThenWaiterDoesNotDeliverBeforeTheCompactionRuns(t *testing.T) {
	var frames []paneFrame
	frames = append(frames, repeatFrame(phaseCaller, captureBusy, 2)...)
	frames = append(frames, repeatFrame(phaseGap, captureIdle, 6)...)
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureReceipt, 6)...)

	engine, script := newScriptedEngine(t, frames)
	engine.waitForSettledTurn(context.Background(), "", "chat", true)

	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: it delivered the steer "+
				"before the compaction ever ran, so the steer dies with the "+
				"pre-compaction session",
			got, phaseDone,
		)
	}
}

// TestThenWaiterDoesNotDeliverIntoResumedWork is the LATE-landing repro, and
// the one that actually happened. The caller does not stop after queueing the
// compaction, so the idle between its turn and the compaction is a single
// blink — shorter than the stability window. The waiter misses it, misses the
// equally brief idle right after the compaction, and finally releases once the
// resumed session goes quiet: the steer lands on top of work already in
// progress, which is the collision this pair of tests exists to prevent.
func TestThenWaiterDoesNotDeliverIntoResumedWork(t *testing.T) {
	var frames []paneFrame
	frames = append(frames, repeatFrame(phaseCaller, captureBusy, 4)...)
	frames = append(frames, paneFrame{phase: phaseGap, capture: captureIdle})
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, paneFrame{phase: phaseDone, capture: captureReceipt})
	frames = append(frames, repeatFrame(phaseResumed, captureBusy, 8)...)
	frames = append(frames, repeatFrame(phaseLate, captureReceipt, 6)...)

	engine, script := newScriptedEngine(t, frames)
	engine.waitForSettledTurn(context.Background(), "", "chat", true)

	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: it slept through the one "+
				"idle that followed the compaction and delivered the steer "+
				"into work that had already resumed",
			got, phaseDone,
		)
	}
}

// TestThenWaiterStillReleasesWithoutACompactionReceipt keeps the fix honest for
// every non-compaction primary (a reload steer, a plain queued message): those
// panes never print a receipt, and a waiter that insisted on one would hang
// until its bound expired and strand the chain. It must still ride out the
// turn — and it must still refuse to mistake the caller's own turn for it.
func TestThenWaiterStillReleasesWithoutACompactionReceipt(t *testing.T) {
	// The pre-compaction gap is deliberately LONGER than the stability window,
	// so a waiter that counts steady idle before it has seen the primary's turn
	// begin releases here and the test catches it. The trailing frames are busy
	// for the same reason in the other direction: a waiter that bounds out
	// without ever deciding ends up in resumed work, not in a phase that would
	// have let it pass by accident.
	var frames []paneFrame
	frames = append(frames, repeatFrame(phaseCaller, captureBusy, 2)...)
	frames = append(frames, repeatFrame(phaseGap, captureIdle, 5)...)
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureIdle, 3)...)
	frames = append(frames, repeatFrame(phaseResumed, captureBusy, 8)...)

	engine, script := newScriptedEngine(t, frames)
	engine.waitForSettledTurn(context.Background(), "", "chat", true)

	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: with no receipt to key on "+
				"it must still ride out the turn that started AFTER the "+
				"caller's own turn ended",
			got, phaseDone,
		)
	}
}

// TestCompactionReceiptNeedsToAppear guards the one way the receipt rule could
// itself become a coincidence detector: a receipt still sitting in the pane
// from a PREVIOUS compaction is not evidence that this one ran. Only a receipt
// that appears while the waiter is watching counts.
func TestCompactionReceiptNeedsToAppear(t *testing.T) {
	var frames []paneFrame
	// The pane already shows an older receipt when the waiter wakes up.
	frames = append(frames, repeatFrame(phaseCaller, captureBusy, 2)...)
	frames = append(frames, repeatFrame(phaseGap, captureReceipt, 4)...)
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureReceipt, 6)...)

	engine, script := newScriptedEngine(t, frames)
	if !strings.Contains(frames[2].capture, "Compacted") {
		t.Fatalf("fixture no longer shows a stale receipt during the gap")
	}
	engine.waitForSettledTurn(context.Background(), "", "chat", true)

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

	observed := engine.waitForSettledTurn(context.Background(), "", "chat", true)

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

// TestSelfCompactScheduleTellsTheCallerToStop covers the result half of the
// stop rule at the layer that actually writes it, so BOTH callers — the
// chat_self_compact MCP tool and `pfm chat self-compact` — are covered by
// one assertion instead of one of them being quietly missed. That miss was
// real: the notice first shipped in the MCP handler only, which left the
// CLI path saying nothing. `pfm chat inject` no longer has a /compact path
// at all (Task C: chat_self_compact / `pfm chat self-compact` own
// compaction, and both share Engine.ScheduleSelfCompact -> this
// ScheduleAfterCurrentTurn call, which is where SelfCompactStopNotice is
// actually appended).
func TestSelfCompactScheduleTellsTheCallerToStop(t *testing.T) {
	fake := &fakeTmux{capture: "Working (10s)\n› Ask Codex to do anything"}
	engine := newTestEngineWith(t, "cx-self-compact", fake, &fakeSpawner{})
	engine.whoami = fakeSelf{identity: resolve.Identity{
		Session:    "cx-self-compact",
		SocketPath: filepath.Join("/tmp", "tmux-jail", "cx-self-compact"),
		Pane:       "%1",
		Engine:     "codex",
		Source:     "test",
	}}

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "self",
		Message: "/compact hold the wave state",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("scheduled self-compaction = %+v", result)
	}
	if !strings.Contains(result.Message, "STOP NOW") {
		t.Fatalf(
			"a queued SELF-compaction does not tell the caller to stop; the "+
				"waiter needs this caller's turn to end so it can tell the "+
				"compaction apart from it.\ngot: %s",
			result.Message,
		)
	}
}

// TestCompactOfAnotherChatDoesNotTellTheCallerToStop keeps the notice from
// becoming noise on the shape it does not apply to. When the target is somebody
// else's pane, the waiter watches THAT pane; what this caller does next cannot
// blur a boundary it is not part of, so telling it to stop would be wrong.
func TestCompactOfAnotherChatDoesNotTellTheCallerToStop(t *testing.T) {
	fake := &fakeTmux{capture: "Working (10s)\n› Ask Codex to do anything"}
	engine := newTestEngineWith(t, "cx-other-compact", fake, &fakeSpawner{})

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold the wave state",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Message, "STOP NOW") {
		t.Fatalf(
			"compacting ANOTHER chat told this caller to stop working; the "+
				"rule applies only to a chat compacting itself.\ngot: %s",
			result.Message,
		)
	}
}

// TestNonSelfWaiterDoesNotWaitOutATurnItDidNotStart pins the boundary of the
// caller-yield rule, and a regression the rule nearly introduced.
//
// Requiring an idle observation BEFORE the primary's turn is correct only when
// the pane being watched is the pane that asked for the wait — a chat compacting
// itself, whose own turn is still running when the waiter wakes. For any other
// target nothing else owns that pane, so its first busy already IS the primary's
// turn. Demanding a prior idle there waits out the primary, then waits again for
// a second turn that never comes, and finally releases on the bounded fallback:
// seconds late, and carrying a "no turn boundary observed" warning that is
// simply false. This is the shape every `--then` steer to another chat takes, so
// the regression would have been the common case, not the exotic one.
func TestNonSelfWaiterDoesNotWaitOutATurnItDidNotStart(t *testing.T) {
	// The pane is already busy with the primary's own turn on the first sample.
	var frames []paneFrame
	frames = append(frames, repeatFrame(phaseCompacting, captureBusy, 3)...)
	frames = append(frames, repeatFrame(phaseDone, captureIdle, 6)...)
	frames = append(frames, repeatFrame(phaseLate, captureBusy, 6)...)

	engine, script := newScriptedEngine(t, frames)
	observed := engine.waitForSettledTurn(context.Background(), "", "chat", false)

	if !observed {
		t.Fatal(
			"waiter reported no turn boundary for a non-self target whose " +
				"turn it watched start and finish — a false warning on the " +
				"most common --then shape",
		)
	}
	if got := script.decidedIn(); got != phaseDone {
		t.Fatalf(
			"waiter released in phase %q, want %q: it treated the primary's "+
				"own turn as somebody else's and waited for a second turn "+
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
	var calls int
	engine.options.Now = func() time.Time {
		calls++
		return start.Add(time.Duration(calls) * time.Second)
	}

	result, err := engine.DeliverThen(context.Background(), "", "chat", []string{"resume"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || !result.Typed {
		t.Fatalf("DeliverThen() = %+v, want a confirmed delivery once the typist went quiet", result)
	}
	if calls < 3 {
		t.Fatalf(
			"delivered before the typist actually went quiet: waitForQuietTypist's clock only advanced %d time(s), want at least 3 (TypistQuiet=3s at 1s/poll)",
			calls,
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
	engine.options.Now = func() time.Time { return start.Add(time.Second) }

	result, err := engine.DeliverThen(context.Background(), "", "chat", []string{"resume"}, false)
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

// TestScheduleSelfCompactComposesPerEngineAndForwardsThen pins Task D's
// shared composition: ScheduleSelfCompact composes "/compact " + focus for
// a Claude self target and the bare "/compact" for a Codex one, and forwards
// the caller's then steer(s) unmodified. Revert to a bare "/compact" for
// every target and the "claude composes the focus" case fails.
func TestScheduleSelfCompactComposesPerEngineAndForwardsThen(t *testing.T) {
	for _, test := range []struct {
		name   string
		engine string
		want   string
	}{
		{name: "claude composes the focus", engine: "", want: "/compact hold the wave state"},
		{name: "codex sends the bare command", engine: string(pfmengine.Codex), want: "/compact"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTmux{capture: "conversation\n❯ "}
			spawner := &fakeSpawner{}
			engine := newTestEngineWith(t, "cc-self-compact-compose", fake, spawner)
			engine.whoami = fakeSelf{identity: resolve.Identity{
				Session:    "self-session",
				SocketPath: filepath.Join("/tmp", "tmux-jail", "cc-self-compact-compose"),
				Pane:       "%1",
				Engine:     test.engine,
			}}
			result, err := engine.ScheduleSelfCompact(
				context.Background(),
				"hold the wave state",
				[]string{"resume the wave"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != 0 {
				t.Fatalf("ScheduleSelfCompact() = %+v", result)
			}
			spawned := spawner.spawned()
			if len(spawned) != 1 || len(spawned[0].Steers) == 0 || spawned[0].Steers[0] != test.want {
				t.Fatalf("composed command = %+v, want primary %q", spawned, test.want)
			}
			if !reflect.DeepEqual(spawned[0].Steers[1:], []string{"resume the wave"}) {
				t.Fatalf("then was not forwarded unmodified: %+v", spawned[0].Steers)
			}
		})
	}
}

// TestScheduleSelfCompactRefusesAnInvalidFocusBeforeScheduling pins Task D's
// validation: an empty, whitespace-only, or multi-line focus is refused
// before anything is scheduled. Revert the validation in
// Engine.ScheduleSelfCompact and this fails: an empty focus reaches the
// spawner as a bare "/compact ".
//
// The ESC- and BEL-carrying cases pin F8 of the merge-gating review: the
// doc comment above this validation (and SelfCompactInput's in
// mcpserv/types.go) claims "no control characters," but the old check only
// excluded \r\n\x00 — three bytes, not the full control-character class —
// so ESC/BEL/the rest of C0/DEL passed through unfiltered into a string
// typed as literal keystrokes. Narrow the check back to
// strings.ContainsAny(focus, "\r\n\x00") and these two cases stop failing.
func TestScheduleSelfCompactRefusesAnInvalidFocusBeforeScheduling(t *testing.T) {
	for _, focus := range []string{
		"", "   ", "line one\nline two",
		"focus with an ESC\x1bbyte", "focus with a BEL\x07byte",
	} {
		fake := &fakeTmux{capture: "conversation\n❯ "}
		spawner := &fakeSpawner{}
		engine := newTestEngineWith(t, "cc-self-compact-validate", fake, spawner)
		engine.whoami = fakeSelf{identity: resolve.Identity{
			Session:    "self-session",
			SocketPath: filepath.Join("/tmp", "tmux-jail", "cc-self-compact-validate"),
			Pane:       "%1",
		}}
		result, err := engine.ScheduleSelfCompact(context.Background(), focus, []string{"resume"})
		if err != nil {
			t.Fatal(err)
		}
		if result.Code != CodeUndelivered || !strings.Contains(result.Message, "focus must be one non-empty line") {
			t.Fatalf("focus %q result = %+v", focus, result)
		}
		if len(spawner.spawned()) != 0 {
			t.Fatalf("invalid focus %q reached the spawner: %+v", focus, spawner.spawned())
		}
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
	engine.options.Now = func() time.Time { return start }

	result, err := engine.DeliverThen(context.Background(), "", "chat", []string{"resume"}, false)
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
