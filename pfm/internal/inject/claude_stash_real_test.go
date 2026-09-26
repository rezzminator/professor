package inject

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestRealClaudeStashSemantics pins Claude Code's Ctrl+S ("chat:stash")
// composer-stash behavior against a REAL `claude` process — REAL-SESSION per
// TESTPLAN.md § "Flows that CANNOT be jailed" (row 28c). Skipped unless
// PFM_REAL_CLAUDE=1: this is never run incidentally. Case 5, which submits a
// real message and spends one real model turn on the operator's own
// account, is gated a second time behind PFM_REAL_CLAUDE_TURN=1.
//
// PINNED OBSERVATIONS (Claude Code v2.1.257, recorded on this host,
// 2026-09-03, tmux 200x50, a freshly-created scratch project directory —
// recorded from the run of this exact sequence that is now also this test's
// assertions; cases 1, 3, and 4 matched on an earlier recorder-mode run too):
//
//  1. A typed, unsubmitted draft + C-s: the composer goes visibly empty on
//     the very next capture, and the status line grows a trailing
//     "› stashed" marker. C-s stashes a non-empty composer.
//  2. C-s on that now-empty composer: the exact draft text reappears in the
//     composer, byte-for-byte, unsubmitted (not auto-submitted), and the
//     "› stashed" status marker is gone. C-s pops the stash back into the
//     composer when the composer is empty.
//  3. Draft + C-s (stash) + literal "/help" + Enter: `/help` opens a
//     full-screen overlay ("Esc to cancel" hint on its last line); Esc
//     closes it back to the ordinary conversation view. After Esc, the
//     ORIGINAL stashed draft is back in the composer — a slash-command
//     submit restores the parked stash exactly the same way an idle C-s pop
//     does. It is not "restored only on a real model-turn submit"; any
//     Enter-confirmed submission out of the composer pops it. No "Draft
//     restored" hint (see case 5) accompanies this restore.
//  4. Draft A + C-s (stash A), type draft B + C-s: the SECOND stash
//     overwrites the first — B is now the (only) parked stash; A is gone
//     with no warning, no toast, and no refusal (the composer just goes
//     empty exactly as case 1 did). C-s on the now-empty composer pops B,
//     never A. There is exactly one stash slot and it is last-write-wins.
//  5. (PFM_REAL_CLAUDE_TURN=1 only) Draft + C-s (stash) + a real message
//     ("reply with exactly: ok") + Enter: once the turn settles and the
//     model's standalone "ok" reply is on screen, the stashed draft is back
//     in the composer — confirming the engine's long-standing "stashed and
//     restored on submit" assumption for an ACTUAL model turn, not just a
//     slash command. This restore is the ONE case where Claude Code prints
//     an explicit "Draft restored" hint near the status line — a real,
//     if undocumented, signal, but one that fired ONLY here and not for the
//     synchronous pops in cases 2-4, so it is not a general-purpose signal
//     to gate on.
//
// Net effect for the typist guard this file backs (engine.go's "Idle panes
// take the ordinary C-s mash guard" comment, and needsStashGuard): C-s
// always protects a PARKED draft, restored on the caller's NEXT confirmed
// submit of any kind — it does not distinguish a live keystroke from a
// stashed one, which is exactly why the typist guard (ClientActivity) has to
// exist as a SEPARATE check upstream of it.
func TestRealClaudeStashSemantics(t *testing.T) {
	if os.Getenv("PFM_REAL_CLAUDE") != "1" {
		t.Skip("REAL-SESSION: set PFM_REAL_CLAUDE=1 to run against a real " +
			"claude process (TESTPLAN.md § Flows that CANNOT be jailed, row 28c)")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not on PATH")
	}

	// tmuxRoot mirrors paths.Resolve()'s own TmuxDir formula — PFM_TMUX_DIR
	// first, else TMUX_TMPDIR/tmux-<uid> — the exact directory the `claude`
	// launcher on PATH resolves its own socket under (paths.go:165). It is
	// NOT paths.Resolve() itself: that refuses to touch the real $HOME
	// inside a test (paths.Home()) unless PFM_TEST_REAL_HOME=1, and this
	// REAL-SESSION test deliberately needs the operator's real $HOME for
	// claude's own auth — only the tmux-directory half of that formula is
	// wanted here. A hardcoded "/tmp/tmux-1000" scanned the wrong
	// directory, and its own scan-error branch failed OPEN silently
	// (t.Logf + return — indistinguishable from "nothing to clean up"),
	// on any host/container/CI runner where the test-running UID is not
	// 1000 — leaking a real, running claude fleet session onto the live
	// machine every time PFM_REAL_CLAUDE=1 ran there.
	tmuxRoot := os.Getenv(paths.EnvTmuxDir)
	if tmuxRoot == "" {
		tmuxBase := os.Getenv("TMUX_TMPDIR")
		if tmuxBase == "" {
			tmuxBase = "/tmp"
		}
		tmuxRoot = filepath.Join(tmuxBase, "tmux-"+strconv.Itoa(os.Getuid()))
	}
	if err := os.MkdirAll(tmuxRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(tmuxRoot, fmt.Sprintf("probe-pfm-stash-%d", os.Getpid()))
	project, err := os.MkdirTemp("/tmp", "pfm-stash-project-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(project); err != nil {
			t.Logf("cleanup: remove scratch project dir: %v", err)
		}
	})

	// Teardown MUST kill the scratch tmux server even when the test fails
	// partway through, so a stuck real claude process never idles as a
	// stray on the box.
	//
	// It must ALSO hunt down and kill whatever pfm's OWN launcher spawned:
	// the `claude` binary on PATH is pfm's managed launcher
	// (~/.local/bin/claude), which execs `pfm internal launch`, and that
	// does not run the chat inside THIS scratch pane — it creates a
	// SEPARATE, independently-detached, persistent `cc-*` tmux server of
	// its own (pfm's normal one-socket-per-chat architecture) and the
	// scratch pane attaches into it. Killing only the scratch socket
	// therefore leaves that second, real, fleet-visible session running
	// forever — this was observed directly during development: five
	// TestRealClaudeStashSemantics runs left five live orphaned `cc-*`
	// sessions behind, each still running after `kill-server` on the
	// scratch socket, found only by manually walking `/tmp/tmux-1000` and
	// cross-checking `#{pane_current_path}` against the scratch project
	// dir. killPfmLaunchedSessions does that walk so teardown catches it
	// automatically instead of leaking a real chat onto the live fleet.
	killed := false
	kill := func() {
		if killed {
			return
		}
		killed = true
		killPfmLaunchedSessions(t, tmuxRoot, project)
		killCmd := exec.Command("tmux", "-S", socket, "kill-server")
		killCmd.Env = append(os.Environ(), "TMUX=")
		_ = killCmd.Run()
		// tmux does not always unlink an explicit -S socket path on
		// kill-server (observed on this host: the server exits but the
		// socket file survives it), and /tmp/tmux-1000 is shared with the
		// real fleet — a dead probe socket left behind is debris there, not
		// just in a scratch dir.
		_ = os.Remove(socket)
	}
	t.Cleanup(kill)

	const session = "pfm-stash-probe"
	start := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", session,
		"-x", "200", "-y", "50", "-c", project,
		"claude",
	)
	start.Env = append(os.Environ(), "TMUX=")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start real claude session: %v: %s", err, output)
	}

	ctx := context.Background()
	tmux := TmuxInjector{}
	capture := func() string {
		text, err := tmux.Capture(ctx, socket, session, false, 0)
		if err != nil {
			t.Fatalf("capture pane: %v", err)
		}
		return text
	}
	sendKey := func(key string) {
		if err := tmux.SendKey(ctx, socket, session, key); err != nil {
			t.Fatalf("send key %q: %v", key, err)
		}
	}
	sendLiteral := func(text string) {
		if err := tmux.SendLiteral(ctx, socket, session, text); err != nil {
			t.Fatalf("send literal %q: %v", text, err)
		}
	}
	const uiSettle = 500 * time.Millisecond
	logCapture := func(label string) string {
		time.Sleep(uiSettle)
		text := capture()
		t.Logf("[%s]\n%s", label, text)
		return text
	}
	// composerTail returns the current composer line: everything after the
	// LAST "❯" in the capture, up to the next newline. capture-pane -J joins
	// wrapped lines, so a wide (200-column) composer stays on one logical
	// line here.
	composerTail := func(text string) string {
		idx := strings.LastIndex(text, "❯")
		if idx == -1 {
			return ""
		}
		tail := text[idx+len("❯"):]
		if newline := strings.IndexAny(tail, "\r\n"); newline != -1 {
			tail = tail[:newline]
		}
		return strings.TrimSpace(tail)
	}
	// stashedMarkerPresent reads the SAME signal production's own
	// DraftStashed heuristic keys off (engine.go:
	// strings.Contains(strings.ToLower(lastLines(capture, 8)), "stashed")).
	// F10 of the merge-gating review: the PINNED OBSERVATIONS comment above
	// claimed a trailing "› stashed" status marker distinguishes case 1 from
	// case 2, but nothing in this test body ever actually read it — so if
	// Claude Code ever renamed that status text, production's heuristic
	// would silently break with zero signal from the one test built to
	// catch exactly that. This closes the gap.
	stashedMarkerPresent := func(text string) bool {
		return strings.Contains(strings.ToLower(captureLastLines(text, 8)), "stashed")
	}
	// clearComposer backspaces whatever is CURRENTLY visible in the composer
	// down to empty, self-correcting against whatever case-to-case semantics
	// turn out to be — it reads the ground-truth capture rather than
	// assuming what should be there.
	clearComposer := func(label string) {
		for attempt := 0; attempt < 200; attempt++ {
			if composerTail(capture()) == "" {
				return
			}
			sendKey("BSpace")
		}
		t.Logf("[%s] WARNING: composer still not empty after 200 backspaces: %q", label, composerTail(capture()))
	}
	// flushStash pops whatever is currently parked (if anything) onto an
	// empty composer and clears it, so the next case starts with an empty
	// composer AND an empty stash slot regardless of what the prior case's
	// semantics turned out to be.
	flushStash := func(label string) {
		clearComposer(label + ": pre-flush composer clear")
		sendKey("C-s")
		popped := logCapture(label + ": C-s on empty composer to flush any leftover stash")
		if composerTail(popped) != "" {
			clearComposer(label + ": post-flush composer clear")
		}
	}

	// --- boot: wait for the composer, handling one trust/onboarding dialog.
	deadline := time.Now().Add(60 * time.Second)
	started := time.Now()
	dialogHandled := false
	var last string
	for {
		last = capture()
		t.Logf("[boot t=%s] capture:\n%s", time.Since(started).Round(100*time.Millisecond), last)
		// "connecting…" in the status line means the composer glyph is
		// already painted but the account/model handshake is not done yet —
		// typing into it here is what produced the one boot-race failure
		// seen while developing this test (the composer silently dropped
		// the first literal send).
		if strings.Contains(last, "❯") && !menuHintPattern.MatchString(last) &&
			!strings.Contains(last, "connecting") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("BLOCKED: claude never reached a composer within 60s; last capture:\n%s", last)
		}
		if !dialogHandled && strings.TrimSpace(last) != "" && time.Since(started) > 2*time.Second {
			t.Logf("[boot] no bare composer yet — treating this as a trust/onboarding dialog, answering Enter")
			sendKey("Enter")
			dialogHandled = true
			time.Sleep(uiSettle)
			t.Logf("[boot] post-dialog-Enter capture:\n%s", capture())
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Logf("composer ready after %s", time.Since(started).Round(100*time.Millisecond))

	// --- case 1: typed draft + C-s -> composer empty.
	draftAlpha := "pfm-stash-case1-alpha"
	sendLiteral(draftAlpha)
	afterType := logCapture("case1: after typing the draft")
	if !strings.Contains(composerTail(afterType), draftAlpha) {
		t.Fatalf("draft never appeared in the composer before C-s:\n%s", afterType)
	}
	sendKey("C-s")
	afterStash := logCapture("case1: after C-s on a non-empty composer")
	if got := composerTail(afterStash); got != "" {
		t.Fatalf("PINNED OBSERVATION 1 violated: C-s on a non-empty composer did not empty it, got %q", got)
	}
	if !stashedMarkerPresent(afterStash) {
		t.Fatalf(
			"PINNED OBSERVATION 1 violated: C-s on a non-empty composer did not grow a \"stashed\" status marker (this is the exact string production's DraftStashed heuristic keys off — engine.go's strings.Contains(strings.ToLower(lastLines(capture, 8)), \"stashed\")):\n%s",
			afterStash,
		)
	}

	// --- case 2: C-s on empty -> draft pops back.
	sendKey("C-s")
	afterPop := logCapture("case2: after C-s on the now-empty composer")
	if got := composerTail(afterPop); got != draftAlpha {
		t.Fatalf(
			"PINNED OBSERVATION 2 violated: C-s on an empty composer did not restore the parked draft, got %q want %q",
			got,
			draftAlpha,
		)
	}
	if stashedMarkerPresent(afterPop) {
		t.Fatalf(
			"PINNED OBSERVATION 2 violated: the \"stashed\" status marker was still present after C-s popped the draft back:\n%s",
			afterPop,
		)
	}
	clearComposer("case2: cleanup")

	// --- case 3: draft + C-s + /help + Enter (+ Esc if an overlay opens) ->
	// is the draft back (command submit)?
	flushStash("pre-case3")
	draftBeta := "pfm-stash-case3-beta"
	sendLiteral(draftBeta)
	logCapture("case3: typed draft beta")
	sendKey("C-s")
	stashedBeta := logCapture("case3: C-s (stash draft beta)")
	if got := composerTail(stashedBeta); got != "" {
		t.Fatalf("case3 setup: composer was not emptied by C-s, got %q", got)
	}
	sendLiteral("/help")
	logCapture("case3: typed /help")
	sendKey("Enter")
	afterHelp := logCapture("case3: after Enter on /help")
	if menuHintPattern.MatchString(afterHelp) || strings.Contains(afterHelp, "Esc to") {
		sendKey("Escape")
		afterHelp = logCapture("case3: after Esc closing the /help overlay")
	}
	if got := composerTail(afterHelp); got != draftBeta {
		t.Fatalf(
			"PINNED OBSERVATION 3 violated: a /help command submit did not restore the parked draft, got %q want %q",
			got,
			draftBeta,
		)
	}
	if strings.Contains(afterHelp, "Draft restored") {
		t.Fatalf(
			"PINNED OBSERVATION 3 violated: a /help command submit printed the \"Draft restored\" hint — that hint is pinned (case 5) to fire ONLY on a real message submit, never on a slash-command submit:\n%s",
			afterHelp,
		)
	}
	clearComposer("case3: cleanup")

	// --- case 4: draft A + C-s, type B + C-s, C-s on empty -> which text
	// pops (overwrite / swap / refuse)?
	flushStash("pre-case4")
	draftA := "pfm-stash-case4-A"
	draftB := "pfm-stash-case4-B"
	sendLiteral(draftA)
	logCapture("case4: typed draft A")
	sendKey("C-s")
	logCapture("case4: C-s after draft A (stash A)")
	sendLiteral(draftB)
	logCapture("case4: typed draft B while A is parked")
	sendKey("C-s")
	afterSecondStash := logCapture("case4: C-s after draft B while A is parked — overwrite/swap/refuse?")
	if got := composerTail(afterSecondStash); got != "" {
		t.Fatalf(
			"PINNED OBSERVATION 4 violated: stashing B while A was parked did not empty the composer (a refusal would leave B behind), got %q",
			got,
		)
	}
	sendKey("C-s")
	afterFinalPop := logCapture("case4: C-s on the now-empty composer — which text pops?")
	if got := composerTail(afterFinalPop); got != draftB {
		t.Fatalf(
			"PINNED OBSERVATION 4 violated: the second stash did not overwrite the first — popped %q, want the LAST-stashed draft %q (draft A must be gone, not swapped back or refused)",
			got,
			draftB,
		)
	}
	clearComposer("case4: cleanup")
	flushStash("post-case4")

	// --- case 5 (PFM_REAL_CLAUDE_TURN=1 only): draft + C-s + a real message
	// + Enter -> once the turn settles and "ok" is on screen, is the draft
	// back (message submit)?
	if os.Getenv("PFM_REAL_CLAUDE_TURN") != "1" {
		t.Log("case5 SKIPPED: set PFM_REAL_CLAUDE_TURN=1 to also spend one " +
			"real model turn pinning message-submit stash-restore semantics")
		return
	}
	draftFive := "pfm-stash-case5-draft"
	sendLiteral(draftFive)
	logCapture("case5: typed draft")
	sendKey("C-s")
	stashedFive := logCapture("case5: C-s (stash the draft)")
	if got := composerTail(stashedFive); got != "" {
		t.Fatalf("case5 setup: composer was not emptied by C-s, got %q", got)
	}
	const sentMessage = "reply with exactly: ok"
	sendLiteral(sentMessage)
	logCapture("case5: typed the real message")
	sendKey("Enter")
	// okWord is matched against the capture with the SENT instruction text
	// stripped first: "reply with exactly: ok" itself contains the word "ok",
	// so matching the raw capture would false-positive on the echo of our own
	// message (in the composer before Enter clears it, and in the transcript
	// after) instead of on the model's actual reply.
	turnDeadline := time.Now().Add(60 * time.Second)
	okWord := regexp.MustCompile(`(?i)\bok\b`)
	sanitize := func(text string) string {
		return strings.ReplaceAll(text, sentMessage, "")
	}
	var final string
	stable := 0
	for {
		current := capture()
		if okWord.MatchString(sanitize(current)) {
			if current == final {
				stable++
			} else {
				stable = 1
			}
			final = current
			if stable >= 2 {
				break
			}
		} else {
			final = current
			stable = 0
		}
		if time.Now().After(turnDeadline) {
			t.Fatalf("case5: turn never settled with a standalone \"ok\" reply within 60s; last capture:\n%s", final)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("case5: final capture after the turn settled:\n%s", final)
	if got := composerTail(final); got != draftFive {
		t.Fatalf(
			"PINNED OBSERVATION 5 violated: a real message submit did not restore the parked draft, got %q want %q",
			got,
			draftFive,
		)
	}
	if !strings.Contains(final, "Draft restored") {
		t.Fatalf(
			"PINNED OBSERVATION 5 violated: a real message submit restored the draft without printing the explicit \"Draft restored\" hint — the one case this hint is pinned to fire on:\n%s",
			final,
		)
	}
	clearComposer("case5: cleanup")
}

// killPfmLaunchedSessions finds and kills any fleet-managed `cc-*` / `cx-*`
// tmux server under root whose pane is running out of project — the second,
// independently-detached session pfm's own launcher spawns (see the comment
// on TestRealClaudeStashSemantics's teardown). Each managed chat is one
// dedicated tmux SERVER (pfm's one-socket-per-chat model), so this walks the
// socket directory itself rather than asking pfm to enumerate them: a probe
// session that never got far enough to be indexed must still be found.
func killPfmLaunchedSessions(t *testing.T, root, project string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		// Fail CLOSED, never open: a scan that could not run is not
		// evidence there was nothing to clean up — pfm/CLAUDE.md's own
		// named anti-pattern is exactly this, "a probe that could not run
		// never returns 'nothing found.'" Silently returning here (t.Logf
		// and no failure) let a real, running claude fleet session leak
		// onto the live machine with the test still reporting green.
		t.Errorf(
			"cleanup: could not scan %q for a launcher-spawned session — cleanup cannot prove it caught everything: %v",
			root,
			err,
		)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "cc-") && !strings.HasPrefix(name, "cx-") {
			continue
		}
		candidate := filepath.Join(root, name)
		list := exec.Command("tmux", "-S", candidate, "list-panes", "-a", "-F", "#{pane_current_path}")
		list.Env = append(os.Environ(), "TMUX=")
		output, listErr := list.Output()
		if listErr != nil {
			// No server on this socket (a dead file left by an earlier run,
			// or one this test never touched) — nothing to kill.
			continue
		}
		if !strings.Contains(string(output), project) {
			continue
		}
		t.Logf("cleanup: killing launcher-spawned session %q (pane cwd matched scratch project %q)", name, project)
		kill := exec.Command("tmux", "-S", candidate, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		if killErr := kill.Run(); killErr != nil {
			t.Logf("cleanup: kill-server on %q: %v", name, killErr)
		}
		_ = os.Remove(candidate)
	}
}
