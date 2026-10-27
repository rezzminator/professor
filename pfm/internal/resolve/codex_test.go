package resolve

import (
	"errors"
	"strings"
	"testing"
)

func codexCandidates() []CodexThread {
	return []CodexThread{
		{
			ID:          "born-earlier",
			CWD:         "/work/project",
			CreatedAt:   1000,
			RolloutPath: "/codex/sessions/rollout-born-earlier.jsonl",
		},
		{
			ID:          "born-nearest",
			CWD:         "/work/project",
			CreatedAt:   1090,
			RolloutPath: "/codex/sessions/rollout-born-nearest.jsonl",
		},
		{
			ID:        "other-directory",
			CWD:       "/work/elsewhere",
			CreatedAt: 1100,
		},
		{
			ID:        "outside-window",
			CWD:       "/work/project",
			CreatedAt: 1221,
		},
	}
}

func TestCodexThreadIDPrefersTheExportedIdentity(t *testing.T) {
	// The birth match would pick born-nearest; the exported identity outranks
	// it, and it is honored even when the state store has not recorded it yet.
	thread, err := CodexThreadID(
		"born-earlier",
		"",
		"/work/project",
		1100,
		codexCandidates(),
	)
	if err != nil {
		t.Fatalf("CodexThreadID(exported) error = %v", err)
	}
	if thread.ID != "born-earlier" ||
		thread.RolloutPath != "/codex/sessions/rollout-born-earlier.jsonl" {
		t.Fatalf("CodexThreadID(exported) = %#v", thread)
	}

	unknown, err := CodexThreadID(
		"not-in-the-store",
		"",
		"/work/project",
		1100,
		codexCandidates(),
	)
	if err != nil {
		t.Fatalf("CodexThreadID(unknown export) error = %v", err)
	}
	if unknown.ID != "not-in-the-store" || unknown.RolloutPath != "" {
		t.Fatalf("CodexThreadID(unknown export) = %#v", unknown)
	}
}

func TestCodexThreadIDFallsBackToDirectoryAndBirth(t *testing.T) {
	thread, err := CodexThreadID("", "", "/work/project/", 1100, codexCandidates())
	if err != nil {
		t.Fatalf("CodexThreadID(birth match) error = %v", err)
	}
	if thread.ID != "born-nearest" {
		t.Fatalf("CodexThreadID(birth match) = %#v, want the nearest birth", thread)
	}

	edge, err := CodexThreadID("", "", "/work/project", 1341, []CodexThread{
		{ID: "exactly-at-the-bound", CWD: "/work/project", CreatedAt: 1221},
	})
	if err != nil {
		t.Fatalf("CodexThreadID(window edge) error = %v", err)
	}
	if edge.ID != "exactly-at-the-bound" {
		t.Fatalf("CodexThreadID(window edge) = %#v, want the %ds bound inclusive",
			edge, CodexBirthWindowSeconds)
	}
}

// The pane's own binding outranks the birth guess: the birth match would
// pick born-nearest (the thread the pane's TUI process happened to start
// closest to), but a pane that has cleared onto a NEWER thread in the same
// directory must resolve to what it actually bound to, not to whichever
// candidate merely sits closest on the clock.
func TestCodexThreadIDPrefersTheBoundIdentityOverTheBirthGuess(t *testing.T) {
	candidates := append(codexCandidates(), CodexThread{
		ID:          "born-after-the-window",
		CWD:         "/work/project",
		CreatedAt:   5000,
		RolloutPath: "/codex/sessions/rollout-born-after-the-window.jsonl",
	})
	thread, err := CodexThreadID("", "born-after-the-window", "/work/project", 1100, candidates)
	if err != nil {
		t.Fatalf("CodexThreadID(bound) error = %v", err)
	}
	if thread.ID != "born-after-the-window" ||
		thread.RolloutPath != "/codex/sessions/rollout-born-after-the-window.jsonl" {
		t.Fatalf("CodexThreadID(bound) = %#v, want the bound thread over the birth match", thread)
	}

	// The bound identity also outranks an exported CODEX_THREAD_ID: it is
	// the reconcile pass's own live observation, the exported variable is
	// merely inherited.
	stale, err := CodexThreadID("born-earlier", "born-after-the-window", "/work/project", 1100, candidates)
	if err != nil {
		t.Fatalf("CodexThreadID(bound over exported) error = %v", err)
	}
	if stale.ID != "born-after-the-window" {
		t.Fatalf("CodexThreadID(bound over exported) = %#v, want the bound thread", stale)
	}

	// An unresolvable bound id still resolves bare rather than falling back
	// to a birth guess — the state store may simply not have caught up yet.
	bare, err := CodexThreadID("", "not-in-the-store", "/work/project", 1100, candidates)
	if err != nil {
		t.Fatalf("CodexThreadID(unknown bound) error = %v", err)
	}
	if bare.ID != "not-in-the-store" || bare.RolloutPath != "" {
		t.Fatalf("CodexThreadID(unknown bound) = %#v, want the bare bound id", bare)
	}
}

func TestCodexThreadIDReportsAMissInsteadOfGuessing(t *testing.T) {
	// 121 seconds past born-nearest and 221 past born-earlier: nothing is
	// close enough, and the other candidate is in a different directory.
	if thread, err := CodexThreadID(
		"",
		"",
		"/work/project",
		1221,
		codexCandidates()[:3],
	); err == nil {
		t.Fatalf("CodexThreadID(no match) = %#v, want an error", thread)
	} else if !strings.Contains(err.Error(), "/work/project") ||
		!strings.Contains(err.Error(), "120s") {
		t.Fatalf("CodexThreadID(no match) error = %v, want the directory and window", err)
	}

	if _, err := CodexThreadID("", "", "", 1100, codexCandidates()); err == nil ||
		!strings.Contains(err.Error(), CodexThreadEnv) {
		t.Fatalf("CodexThreadID(no directory) error = %v, want a clean refusal", err)
	}
	if _, err := CodexThreadID("", "", "/work/project", 0, codexCandidates()); err == nil {
		t.Fatal("CodexThreadID(no birth time) = nil error, want a clean refusal")
	}
}

func TestCodexThreadSeat(t *testing.T) {
	seat := CodexSeat{
		ThreadID:       "019a2b",
		Backend:        "herdr",
		SeatID:         "s1",
		SocketPath:     "/h/herdr.sock",
		Pane:           "wA:p2",
		HerdrWorkspace: "wA",
	}
	got, err := CodexThreadSeat("019a2b", []CodexSeat{seat})
	if err != nil || got.Session != "s1" || got.SeatID != "s1" || got.Backend != "herdr" ||
		got.SocketName != "herdr.sock" ||
		got.Engine != "cx" ||
		got.ID != "019a2b" ||
		got.Source != "codex-thread" ||
		!got.Recovered {
		t.Fatalf("identity=%+v err=%v", got, err)
	}
	if _, err := CodexThreadSeat("019a2b", []CodexSeat{seat, seat}); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("duplicates: %v", err)
	}
	if _, err := CodexThreadSeat(
		"019a2b",
		nil,
	); !errors.Is(err, ErrNotInChat) ||
		!strings.Contains(err.Error(), "no live Codex seat holds thread 019a2b") {
		t.Fatalf("absent: %v", err)
	}
	seat.BackendError = "herdr unreachable"
	if _, err := CodexThreadSeat(
		"019a2b",
		[]CodexSeat{seat},
	); err == nil || !strings.Contains(err.Error(), seat.BackendError) ||
		errors.Is(err, ErrNotInChat) {
		t.Fatalf("failed read: %v", err)
	}
}
