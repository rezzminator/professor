package inject

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// TestSettledTurnRetriesTheBaselineUntilThePaneWasReallyRead is F1. The
// baseline went through the same sampler as every other poll, and a failed
// capture returned the zero sample — so "the pane could not be read" arrived
// at the receipt door spelled "no compaction receipt was on screen", the one
// affirmative claim that opens it. One transient capture-pane failure at the
// baseline therefore converted the guard into its opposite: the first
// successful sample showed a STALE receipt plus the caller's own busy footer
// and was accepted as this turn's proof, and the steer went into the caller's
// live turn while the result still reported the strong guarantee.
//
// The baseline is now retried on the poll cadence until one real read lands,
// and the doors stay shut until it does.
func TestSettledTurnRetriesTheBaselineUntilThePaneWasReallyRead(t *testing.T) {
	transient := errors.New("no server running on /tmp/tmux-jail/cc-1-2-3")
	var frames []paneFrame
	frames = append(frames, paneFrame{phase: phaseCaller, err: transient})
	// Every later frame is the caller's own turn running over a receipt left
	// by an EARLIER compaction: busy, receipt on screen, nothing new printed.
	frames = append(frames, repeatFrame(phaseCaller, captureBusyReceipt, 12)...)

	engine, script := newScriptedEngine(t, frames)
	engine.options.ThenBusyTries = 3
	engine.options.ThenIdleTries = 6
	observed, err := engine.waitForSettledTurn(context.Background(), "", "chat", true, pfmengine.Claude)
	if err != nil {
		t.Fatalf("waitForSettledTurn() errored although a later capture succeeded: %v", err)
	}
	if observed {
		t.Fatal(
			"waiter treated a FAILED baseline capture as proof no receipt was on " +
				"screen and then took the stale receipt in its first real sample as " +
				"this turn's — it would type into the caller's own live turn",
		)
	}
	if script.served < 3 {
		t.Fatalf("waiter gave up after %d samples without retrying the baseline", script.served)
	}
}

// TestDeliverThenReportsAPaneItNeverReadOnce is F1's other end: when the pane
// could not be read even once, the waiter has no reference frame at all.
// "false" would be a verdict about the turn — a weaker guarantee, delivered
// with a WARNING — and this is not that. Nothing is typed, and the visible
// result names the pane and the tmux error.
func TestDeliverThenReportsAPaneItNeverReadOnce(t *testing.T) {
	blind := errors.New("can't find pane: %9")
	fake := &fakeTmux{capture: captureIdle, submitOnEnter: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-blind", fake, spawner)
	script := &paneScript{fakeTmux: fake, frames: repeatFrame(phaseCaller, "", 1)}
	script.frames[0].err = blind
	engine.tmux = script
	engine.options.ThenMin = time.Nanosecond
	engine.options.ThenIdlePoll = time.Nanosecond
	engine.options.ThenSettle = time.Nanosecond
	engine.options.ThenBusyTries = 2
	engine.options.ThenIdleTries = 2
	engine.options.ThenIdleStable = 1

	result, err := engine.DeliverThen(context.Background(), ThenWait{
		SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-blind"),
		Target:     "%1",
		Steers:     []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != CodeUndelivered || result.Status != statusUndelivered {
		t.Fatalf(
			"DeliverThen() = %+v over a pane that never answered one capture; want a Code %d undelivered result, never a success",
			result,
			CodeUndelivered,
		)
	}
	if !strings.Contains(result.Message, "could not read pane") ||
		!strings.Contains(result.Message, blind.Error()) ||
		!strings.Contains(result.Message, "resume the wave") {
		t.Fatalf(
			"undelivered message %q names neither the unreadable pane, the tmux error, nor the kept steer",
			result.Message,
		)
	}
	if len(fake.keys) != 0 || len(fake.literals) != 0 {
		t.Fatalf("typed into a pane that was never read once: keys=%q literals=%q", fake.keys, fake.literals)
	}
}

// TestSettledTurnRefusesAReceiptThatMerelyScrolledBackIntoView is F2. The
// busy-side door — the one that lets a compaction beside a background agent
// count, since such a pane never reads idle — asked only whether the receipt
// was absent from the baseline capture. Absent from a capture is not the same
// as not yet printed: a receipt clipped out of the visible fold (a footer
// collapse, a pane resize, a redraw) and scrolling back in later satisfies
// that test exactly, and the steer lands in the caller's own turn.
//
// The fixture is that clipping, spelled as the pane really renders it: the
// receipt is in the pane's history the whole time, and only the VISIBLE fold
// changes. The count over history is the second signal — it cannot rise for a
// receipt that was already printed.
func TestSettledTurnRefusesAReceiptThatMerelyScrolledBackIntoView(t *testing.T) {
	history := "Compacted (ctrl+o to see full summary)\n" +
		strings.Repeat("... earlier conversation\n", 30) +
		"working on it\n  esc to interrupt\n❯ "
	clipped := "... earlier conversation\nworking on it\n  esc to interrupt\n❯ "
	uncovered := "Compacted (ctrl+o to see full summary)\nworking on it\n  esc to interrupt\n❯ "

	var frames []paneFrame
	// The baseline and the first polls: the receipt is in history but clipped
	// out of the visible fold.
	frames = append(frames, repeatFrame(phaseCaller, history, 3)...)
	for index := range frames {
		frames[index].visible = clipped
	}
	// The same receipt scrolls back into view while the caller's turn is still
	// running. Nothing was printed; the fold moved.
	scrolled := repeatFrame(phaseCaller, history, 12)
	for index := range scrolled {
		scrolled[index].visible = uncovered
	}
	frames = append(frames, scrolled...)

	engine, script := newScriptedEngine(t, frames)
	engine.options.ThenBusyTries = 3
	engine.options.ThenIdleTries = 6
	if observed := mustSettle(t, engine, true); observed {
		t.Fatal(
			"waiter accepted a receipt that was only newly VISIBLE as proof this " +
				"turn's compaction ran — the pane was busy with the CALLER's turn " +
				"throughout, so the steer would land inside it",
		)
	}
	if script.served < 3 {
		t.Fatalf("waiter gave up after %d samples without exhausting its budget", script.served)
	}
}

// TestSettledTurnCountsReceiptsOverHistoryNotTheVisibleFold pins the sampler
// itself: busy is read from the pane's live footer (an "esc to interrupt"
// retained in scrollback from a turn that ended long ago is not this pane
// being busy), while receipts are counted over the whole captured history.
func TestSettledTurnCountsReceiptsOverHistoryNotTheVisibleFold(t *testing.T) {
	capture := "Compacted (ctrl+o to see full summary)\n" +
		"  esc to interrupt\n" +
		strings.Repeat("... earlier conversation\n", paneBusyTailLines+5) +
		"Compacted (ctrl+o to see full summary)\n❯ "
	wait := SettledTurn{
		Capture: func(context.Context) (string, error) { return capture, nil },
		Sleep:   func(context.Context, time.Duration) {},
		Pane:    "%1",
	}
	sample, err := wait.samplePane(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sample.receipts != 2 {
		t.Fatalf(
			"samplePane().receipts = %d, want 2 — the count is taken over the captured history",
			sample.receipts,
		)
	}
	if sample.busy {
		t.Fatal(
			"samplePane().busy is true from a spinner line retained far up in " +
				"scrollback — busy is a property of the live footer",
		)
	}
}

// idleAfterAgentRecord is an idle Claude pane whose busy window still holds the
// transcript record a finished background agent leaves behind. Its "· 46s"
// matches busyPattern's `· \d+s` arm, and the "Read 27,615 tokens" record
// matches `\d+ tokens`; both are history, not a live turn.
const idleAfterAgentRecord = "● Agent \"Commit release fixes\" finished · 46s\n" +
	"⏺ Read 27,615 tokens from the notes\n" +
	"  Compaction is queued with its steer to follow.\n" +
	"─────────────────────────\n❯ \n─────────────────────────\n" +
	"  🟢 ▱▱▱▱ 8% │ .professor │ develop\n"

// TestIsFooterBusyIgnoresTranscriptRecords is the 2026-09-25 self-compact that
// typed its /compact minutes late: a record line inside the busy window made
// the idle pane read busy, so the waiter never saw the caller yield. A record
// line is transcript; only the live spinner and footer say a turn is running.
func TestIsFooterBusyIgnoresTranscriptRecords(t *testing.T) {
	cases := []struct {
		name    string
		capture string
		want    bool
	}{
		{"idle pane under an agent-finished record", idleAfterAgentRecord, false},
		{"idle pane under a tokens record", "⏺ Read 27,615 tokens\n❯ \n", false},
		{"live spinner with seconds and tokens", idleAfterAgentRecord + "✻ Working… (12s · ↓ 100 tokens)\n", true},
		{"live spawning line", "● Agent \"X\" finished · 46s\n  Spawning … · 51s\n❯ \n", true},
		{"esc to interrupt footer", "● done · 3s\n  esc to interrupt\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFooterBusy(pfmengine.Claude, tc.capture); got != tc.want {
				t.Fatalf("IsFooterBusy(%q) = %t, want %t", tc.capture, got, tc.want)
			}
		})
	}
}

// TestSelfWaiterSeesTheYieldPastATranscriptRecord drives Run over a pane that
// goes busy, then idle with an agent-finished record in its busy window, and
// stays idle. The waiter must see the caller yield and release on the
// steady-idle fallback — within its busy bound plus the stability window,
// counted in polls, not after its whole ten-minute budget.
func TestSelfWaiterSeesTheYieldPastATranscriptRecord(t *testing.T) {
	const busyTries, idleTries, idleStable = 25, 1500, 3
	captures := 0
	wait := SettledTurn{
		Capture: func(context.Context) (string, error) {
			captures++
			if captures <= 3 {
				return captureBusy, nil
			}
			return idleAfterAgentRecord, nil
		},
		Sleep:      func(context.Context, time.Duration) {},
		Pane:       "%0",
		Engine:     pfmengine.Claude,
		SelfTarget: true,
		BusyTries:  busyTries,
		IdleTries:  idleTries,
		IdleStable: idleStable,
	}
	observed, err := wait.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() errored over a readable pane: %v", err)
	}
	if observed {
		t.Fatal("Run() claimed a turn boundary although no turn started after the yield")
	}
	if bound := 3 + busyTries + idleStable + 3; captures > bound {
		t.Fatalf(
			"self waiter released after %d captures, want at most %d: the idle pane read busy "+
				"from a transcript record, so the caller's yield was never seen",
			captures, bound,
		)
	}
}
