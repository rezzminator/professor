package spawn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/naming"
)

// The Codex rename markers below are read from the codex binary's own strings
// (codex-cli 0.147, re-read against 0.154): the slash command is "rename",
// described as "rename the current thread", and its modal is titled "Name
// thread" or "Rename thread" — its "Type a name and press Enter" hint shows
// only while the field is empty, and a thread that already has a name opens
// the field PRE-FILLED. Codex has no launch flag for a thread name, so this UI
// is the only way to set one — and if a Codex release changes the wording,
// every wait below times out, the chat is reported with a warning, and nothing
// is typed blindly into a composer that would have sent it to the model.
//
// 0.154 also stopped announcing a finished rename ("Session renamed to" is
// gone from the binary), so the screen can no longer prove one. The proof is
// Codex's own session_index.jsonl (UseCodexHomes); the announcement and
// a status line carrying the name stay as the second witness.
const (
	codexRenameCommand = "/rename"
	codexRenameOffered = "rename the current thread"
	codexRenamePrompt  = "Type a name and press Enter"
	codexNameTitle     = "Name thread"
	codexRenameTitle   = "Rename thread"
	codexRenameEmpty   = "Thread name cannot be empty."
	codexRenameDone    = "Session renamed to"
	codexTrustQuestion = "Do you trust the contents of this directory?"
	codexTrustYes      = "1. Yes, continue"

	// modalClearKeys is how many BSpace presses clear the rename field. Codex
	// PRE-FILLS it with the thread's current name, so typing straight into it
	// APPENDS — a retry after a half-finished attempt produced
	// "_KILL probeTIMING TEST" on a live box. Extra backspaces on an empty
	// field are ignored, so over-clearing is free and under-clearing is not.
	modalClearKeys = 80

	// confirmPresses is how many times the modal's Enter is re-sent before the
	// rename is called unconfirmed.
	confirmPresses = 3

	// codexComposer and codexStatus are the two halves of "this chat will
	// receive what I type". BOTH are required, and neither is enough alone:
	//
	//   - "›" starts the composer's input line — but it also marks the
	//     SELECTED ROW of Codex's modals, so a trust dialog matches it. That
	//     false positive is what sent a "/rename" and a first prompt into a
	//     hooks-review modal on a live box.
	//   - the status line's token meter renders only under an idle composer;
	//     every startup overlay observed (hooks review, trust selection)
	//     paints over it.
	//
	// A modal that somehow matched both would still be survivable: Escape is
	// the DECLINE path on each of them ("esc to close", "esc to go back"), and
	// this package never presses `t` or Enter on a screen it has not confirmed.
	codexComposer = "›"
	codexStatus   = "% used"
	codexPrompt   = "› Ask Codex to do anything"

	// startupEscapes bounds how many overlays are dismissed before giving up,
	// so a screen that is simply slow is never escaped forever.
	startupEscapes = 4

	// composerHoldReads is how many consecutive reads must show the composer
	// before it counts as ready. Codex paints its composer FIRST and its
	// startup overlays a beat later, so a single sighting is a flash, not a
	// state — that flash is what sent a "/rename" into a hooks modal.
	composerHoldReads = 4

	// renameAttempts covers an overlay that appears between the composer and
	// the keystroke: dismiss it and try the whole rename again rather than
	// declaring a Codex that plainly has /rename incapable of it.
	renameAttempts = 3

	// renameClockSlack widens the proof window below the rename's start, so a
	// ledger timestamp truncated or stamped a hair early still counts.
	renameClockSlack = time.Second

	// promptSubmitTries is how many times the launch prompt's Enter is re-sent
	// before delivery is called unproven — the same lesson confirmPresses
	// carries, applied to the message that MATTERS. A rename that silently
	// fails leaves a working chat with a poor name; a prompt whose Enter is
	// swallowed leaves a chat that costs a seat, answers nothing, and reports
	// success. One unverified keystroke is not a delivery.
	promptSubmitTries = 4

	// composerNeedleMax bounds the fingerprint taken from a prompt's first
	// line: long enough not to collide with a placeholder hint, short enough
	// to survive a composer that wraps or elides.
	composerNeedleMax = 48
)

// Run creates the detached session and, for Codex, drives its rename UI. The
// session is left running in every case a chat exists: a rename that could not
// be completed is a warning on a live chat, never a reason to kill it.
func Run(
	ctx context.Context,
	tmux Tmux,
	request Request,
) (Result, error) {
	if tmux == nil {
		return Result{}, errors.New("spawn requires a tmux client")
	}
	if request.Socket == "" || request.Run == "" || request.CWD == "" {
		return Result{}, errors.New("spawn requires a socket, command and directory")
	}
	launcher, err := LauncherFor(request.Engine)
	if err != nil {
		return Result{}, err
	}
	timings := request.Timings.orDefaults()
	trace := newTracer(request.Trace, clock.Real.Now())
	window := WindowName(request.Name)
	spec := SessionSpec{
		Socket:  request.Socket,
		Session: request.Socket,
		Window:  window,
		CWD:     request.CWD,
		Run:     request.Run,
		Binary:  request.Binary,
		Width:   request.Width,
		Height:  request.Height,
	}
	if spec.Width <= 0 {
		spec.Width = 220
	}
	if spec.Height <= 0 {
		spec.Height = 50
	}
	if err := tmux.NewSession(ctx, spec); err != nil {
		return Result{}, err
	}

	result := Result{
		Socket:  request.Socket,
		Session: spec.Session,
		Window:  window,
		Name:    request.Name,
	}
	target := spec.Session
	trace.step("session %s created, running: %s", spec.Session, request.Run)
	boot, err := waitForBoot(ctx, tmux, request.Socket, target, timings)
	if err != nil {
		return result, err
	}
	trace.step("booted | %s", screen(boot))
	// Nothing is typed until a composer is on screen and STAYS there. A
	// startup overlay swallows every keystroke sent to it — that is how a
	// chat ended up unnamed AND unprompted, with its "/rename" and its first
	// prompt both eaten by a hooks-review modal.
	warning, renameErr := launcher.Rename(ctx, tmux, request.Socket, target, request.Name, timings, trace)
	if renameErr != nil {
		return result, renameErr
	}
	result.Named = warning == ""
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
		capture, captureErr := tmux.Capture(ctx, request.Socket, target)
		if captureErr != nil || !launcher.ComposerReady(capture) {
			return result, nil
		}
	}
	if request.PromptOnCommandLine {
		result.Prompted = request.Prompt != ""
		return result, nil
	}
	if request.Prompt == "" {
		return result, nil
	}
	// An overlay can arrive at any moment during startup (MCP notices land
	// asynchronously), so the composer is re-confirmed before the prompt is
	// typed, exactly as it was before the rename.
	if !waitForComposer(ctx, tmux, request.Socket, target, timings, trace, launcher.ComposerReady) {
		result.Warnings = append(
			result.Warnings,
			"a startup screen is holding the chat — the first prompt was not "+
				"delivered; attach it and clear the screen by hand",
		)
		return result, nil
	}
	if err := submitPrompt(
		ctx,
		tmux,
		request.Socket,
		target,
		request.Prompt,
		timings,
		trace,
	); err != nil {
		result.Warnings = append(
			result.Warnings,
			fmt.Sprintf("the first prompt was not delivered: %v", err),
		)
		return result, nil
	}
	result.Prompted = true
	trace.step("prompt submitted")
	return result, nil
}

// waitForBoot returns once the pane has drawn something and stopped changing,
// which is the only readiness signal both engines share. A capture error means
// the session is gone — the chat died at birth, and saying so beats reporting
// a socket nothing is listening on.
func waitForBoot(
	ctx context.Context,
	tmux Tmux,
	socket, target string,
	timings Timings,
) (string, error) {
	deadline := clock.Real.Now().Add(timings.Boot)
	previous := ""
	settled := false
	for {
		capture, err := tmux.Capture(ctx, socket, target)
		if err != nil {
			return "", fmt.Errorf(
				"the chat died at birth on socket %s: %w",
				socket,
				err,
			)
		}
		trimmed := strings.TrimSpace(capture)
		if trimmed != "" && trimmed == previous {
			if settled {
				return capture, nil
			}
			settled = true
		} else {
			settled = false
		}
		previous = trimmed
		if clock.Real.Now().After(deadline) {
			if trimmed == "" {
				return "", fmt.Errorf(
					"the chat drew nothing within %s on socket %s",
					timings.Boot,
					socket,
				)
			}
			return capture, nil
		}
		if err := sleep(ctx, timings.Poll); err != nil {
			return "", err
		}
	}
}

// composerReady reports whether a capture shows an idle composer.
func composerReady(capture string) bool {
	if !strings.Contains(capture, codexComposer) {
		return false
	}
	if strings.Contains(capture, codexStatus) {
		return true
	}
	// Codex 0.149's default status line no longer includes a context percent.
	// Its empty-composer prompt plus the model/CWD footer is the equivalent
	// two-part proof: startup modals can select a › row, but do not render this
	// prompt followed by the idle footer.
	lines := strings.Split(capture, "\n")
	for index, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), codexPrompt) {
			continue
		}
		for footerIndex := index + 1; footerIndex < len(lines) && footerIndex <= index+4; footerIndex++ {
			footer := strings.TrimSpace(lines[footerIndex])
			if footer != "" && strings.Contains(footer, " · ") &&
				!strings.HasPrefix(footer, codexComposer) {
				return true
			}
		}
	}
	return false
}

// waitForCodexComposer returns once the composer is drawn, dismissing startup
// overlays along the way. Most overlays dismiss with Escape, but Codex
// 0.149's directory-trust dialog makes Escape quit the whole TUI; its exact
// affirmative row is accepted with Enter. A key is sent only once the screen
// has stopped changing, so a slow paint is never mistaken for a stuck modal.
func waitForCodexComposer(
	ctx context.Context,
	tmux Tmux,
	socket, target string,
	timings Timings,
	trace tracer,
) bool {
	return waitForComposer(ctx, tmux, socket, target, timings, trace, composerReady)
}

func waitForComposer(
	ctx context.Context,
	tmux Tmux,
	socket, target string,
	timings Timings,
	trace tracer,
	ready func(string) bool,
) bool {
	deadline := clock.Real.Now().Add(timings.Boot)
	previous := ""
	dismissals := 0
	held := 0
	for {
		capture, err := tmux.Capture(ctx, socket, target)
		switch {
		case err == nil && ready(capture):
			held++
			if held >= composerHoldReads {
				trace.step("composer held %d reads | %s", held, screen(capture))
				return true
			}
		case err == nil:
			// An overlay: dismiss it once the screen has stopped changing, so
			// a half-drawn frame is never mistaken for a stuck modal.
			held = 0
			trimmed := strings.TrimSpace(capture)
			if trimmed != "" && trimmed == previous && dismissals < startupEscapes {
				key := startupOverlayKey(capture)
				trace.step("overlay %d dismissed with %s | %s", dismissals+1, key, screen(capture))
				_ = tmux.SendKey(ctx, socket, target, key)
				dismissals++
				previous = ""
			} else {
				previous = trimmed
			}
		default:
			held = 0
		}
		if clock.Real.Now().After(deadline) {
			return false
		}
		if err := sleep(ctx, timings.Poll); err != nil {
			return false
		}
	}
}

func startupOverlayKey(capture string) string {
	if strings.Contains(capture, codexTrustQuestion) && strings.Contains(capture, codexTrustYes) {
		return "Enter"
	}
	return "Escape"
}

// nameCodexThread waits for a composer that holds, then renames — retrying the
// pair when an overlay lands between the two. Codex draws its composer before
// its startup modals, so "composer, then modal, then keystroke" is a real
// ordering, not a hypothetical one.
// The blocked return says the composer was never reachable, which is a
// different verdict from "renamed nothing": nothing was typed at all, so the
// caller must not try the prompt either.
//
// A retry is a second /rename into a modal that is now PRE-FILLED with the
// name the first attempt may already have set, so the ledger is asked first:
// a rename it has recorded is done, and one it cannot be asked about is not
// retried blind.
func nameCodexThread(
	ctx context.Context,
	tmux Tmux,
	socket, target, name string,
	timings Timings,
	trace tracer,
	proof renameProof,
) (named bool, warning string, blocked bool) {
	since := clock.Real.Now().Add(-renameClockSlack)
	for attempt := 0; attempt < renameAttempts; attempt++ {
		if attempt > 0 && proof != nil {
			landed, err := proof(name, since)
			if err != nil {
				return false, unverifiedRename(err), false
			}
			if landed {
				trace.step("rename proven by Codex's session index before retry %d", attempt+1)
				return true, "", false
			}
		}
		trace.step("rename attempt %d/%d", attempt+1, renameAttempts)
		if !waitForCodexComposer(ctx, tmux, socket, target, timings, trace) {
			return false, "Codex is still holding a startup screen — nothing " +
				"was typed into it, so the chat is unnamed and unprompted; " +
				"attach it and clear the screen by hand", true
		}
		renamed, why, unverifiable := renameCodexThread(ctx, tmux, socket, target, name, timings, trace, proof, since)
		if renamed {
			return true, "", false
		}
		warning = why
		if unverifiable {
			return false, warning, false
		}
	}
	// The ledger write can trail the last screen poll; ask it once more before
	// calling a chat unnamed.
	if proof != nil {
		if landed, err := proof(name, since); err == nil && landed {
			trace.step("rename proven by Codex's session index after the last attempt")
			return true, "", false
		}
	}
	return false, warning, false
}

// unverifiedRename is the verdict when neither witness can speak: Codex shows
// nothing on screen and its ledger could not be read. That is "could not
// verify", never "unnamed" — the rename may well have landed.
func unverifiedRename(err error) string {
	return fmt.Sprintf(
		"could not verify the Codex rename — nothing on screen confirms it and its session index could not be read (%v); the chat may be named, check it with pfm ls",
		err,
	)
}

// renameCodexThread drives Codex's own rename UI and verifies each step before
// taking the next one. It reports the warning rather than an error: an unnamed
// chat is still a working chat.
//
// unverifiable reports that the rename's outcome could not be established
// either way, which a retry cannot fix.
func renameCodexThread(
	ctx context.Context,
	tmux Tmux,
	socket, target, name string,
	timings Timings,
	trace tracer,
	proof renameProof,
	since time.Time,
) (renamed bool, warning string, unverifiable bool) {
	if err := tmux.SendLiteral(ctx, socket, target, codexRenameCommand); err != nil {
		return false, fmt.Sprintf("could not type the rename command: %v", err), false
	}
	trace.step("typed %s", codexRenameCommand)
	if !waitFor(ctx, tmux, socket, target, codexRenameOffered, timings) {
		capture, _ := tmux.Capture(ctx, socket, target)
		trace.step("no /rename offer | %s", screen(capture))
		leftover := clearComposer(
			ctx,
			tmux,
			socket,
			target,
			codexRenameCommand,
		)
		warning := "this Codex build did not offer /rename — the chat is running unnamed"
		if leftover {
			warning += "; its composer may still hold " + codexRenameCommand
		}
		return false, warning, false
	}
	if err := tmux.SendKey(ctx, socket, target, "Enter"); err != nil {
		return false, fmt.Sprintf("could not open the rename prompt: %v", err), false
	}
	if !pollCapture(ctx, tmux, socket, target, timings, renameModalOpen) {
		capture, _ := tmux.Capture(ctx, socket, target)
		trace.step("no name prompt | %s", screen(capture))
		_ = tmux.SendKey(ctx, socket, target, "Escape")
		return false, "Codex never asked for a thread name — the chat is running unnamed", false
	}
	for index := 0; index < modalClearKeys; index++ {
		if err := tmux.SendKey(ctx, socket, target, "BSpace"); err != nil {
			return false, fmt.Sprintf("could not clear the name field: %v", err), false
		}
	}
	if err := tmux.SendLiteral(ctx, socket, target, name); err != nil {
		return false, fmt.Sprintf("could not type the thread name: %v", err), false
	}
	// Success is PROVEN, never inferred from a modal that merely closed: by
	// Codex's own ledger (0.154 renames silently), or by the transcript's
	// "• Session renamed to X." an older Codex prints.
	//
	// The Enter is re-sent while the modal stands: a TUI reading its input in
	// bursts can drop a confirmation that arrives glued to the text, and one
	// extra Enter on a modal that already closed lands on an empty composer,
	// where it does nothing.
	trace.step("typed the name")
	var proofErr error
	landed := renameLanded(name)
	witnessed := func(capture string) bool {
		if landed(capture) {
			return true
		}
		if proof == nil {
			return false
		}
		recorded, err := proof(name, since)
		proofErr = err
		return recorded
	}
	confirmed := false
	for press := 0; press < confirmPresses && !confirmed; press++ {
		if err := sleep(ctx, timings.Typed); err != nil {
			break
		}
		if err := tmux.SendKey(ctx, socket, target, "Enter"); err != nil {
			return false, fmt.Sprintf("could not confirm the thread name: %v", err), false
		}
		confirmed = pollCapture(
			ctx,
			tmux,
			socket,
			target,
			Timings{Poll: timings.Poll, Step: confirmWait(timings)},
			witnessed,
		)
		if !confirmed {
			trace.step("confirmation press %d did not take", press+1)
		}
	}
	if !confirmed {
		// Read the reason BEFORE dismissing the prompt: Escape takes the
		// modal — and the refusal printed inside it — off the screen.
		warning := "Codex never confirmed the rename — the chat may be unnamed"
		unverifiable := false
		capture, _ := tmux.Capture(ctx, socket, target)
		switch {
		case strings.Contains(capture, codexRenameEmpty):
			warning = "Codex refused the name as empty — the chat is running unnamed"
		case proofErr != nil:
			warning, unverifiable = unverifiedRename(proofErr), true
		}
		trace.step("rename unconfirmed: %s | %s", warning, screen(capture))
		_ = tmux.SendKey(ctx, socket, target, "Escape")
		return false, warning, unverifiable
	}
	trace.step("rename confirmed")
	return true, "", false
}

// renameLanded is the proof a rename took: Codex's own announcement, or the
// status line carrying the new name. The status line is the stronger of the
// two — it is still true a minute later, while an announcement scrolls away.
func renameLanded(name string) func(string) bool {
	return func(capture string) bool {
		return strings.Contains(capture, codexRenameDone) ||
			(composerReady(capture) && strings.Contains(capture, name+" · "))
	}
}

// renameModalOpen recognizes Codex's rename dialog by its empty-field hint or
// by its title. The hint alone missed every dialog opened on a thread that
// already had a name: 0.154 pre-fills the field and hides the hint, leaving
//
//	▌ Rename thread
//	▌ Generating a title suggestion…
//	▌
//	▌ PING_PROBE
//	Press enter to confirm or esc to go back
//
// A title counts only as a whole line — whatever gutter glyph Codex draws
// before it — AND only while no composer is on screen: the dialog paints over
// the composer. Title text anywhere else is transcript, and mistaking it for
// the dialog would clear and type the name into a live composer, which sends it
// to the model as a prompt.
func renameModalOpen(capture string) bool {
	if strings.Contains(capture, codexRenamePrompt) {
		return true
	}
	if composerReady(capture) {
		return false
	}
	for _, line := range strings.Split(capture, "\n") {
		title := strings.TrimLeftFunc(line, func(r rune) bool { return !unicode.IsLetter(r) })
		title = strings.TrimSpace(title)
		if title == codexNameTitle || title == codexRenameTitle {
			return true
		}
	}
	return false
}

// confirmWait bounds one confirmation press, so a dropped Enter costs a
// fraction of the step budget instead of all of it.
func confirmWait(timings Timings) time.Duration {
	wait := timings.Step / confirmPresses
	if wait < timings.Poll*2 {
		wait = timings.Poll * 2
	}
	return wait
}

// submitPrompt types the launch prompt and PROVES it left the composer.
//
// The pause between the text and the Enter is what keeps a TUI from receiving
// the newline before it has processed the text — the same gap chat.sh leaves
// when it injects. The re-sends after it are what keep a dropped newline from
// passing as a delivery: an engine still finishing its MCP boot reads its
// input in bursts, and the burst that carries a lone Enter is the one it
// misses.
func submitPrompt(
	ctx context.Context,
	tmux Tmux,
	socket, target, text string,
	timings Timings,
	trace tracer,
) error {
	if err := tmux.SendLiteral(ctx, socket, target, text); err != nil {
		return err
	}
	needle := composerNeedle(text)
	step := Timings{Poll: timings.Poll, Step: confirmWait(timings)}
	if !pollCapture(ctx, tmux, socket, target, step, func(capture string) bool {
		return composerHolds(capture, needle)
	}) {
		// Not fatal on its own: a composer that elides or re-wraps what it was
		// given can kill the fingerprint. It is recorded because it makes the
		// difference between "the Enter was dropped" and "the text never
		// arrived" readable after the fact.
		trace.step("the composer never showed the prompt")
	}
	for press := 0; press < promptSubmitTries; press++ {
		if err := sleep(ctx, timings.Typed); err != nil {
			return err
		}
		if err := tmux.SendKey(ctx, socket, target, "Enter"); err != nil {
			return err
		}
		if pollCapture(ctx, tmux, socket, target, step, func(capture string) bool {
			return !composerHolds(capture, needle)
		}) {
			trace.step("prompt left the composer on press %d", press+1)
			return nil
		}
		trace.step("submit press %d did not take", press+1)
	}
	return fmt.Errorf(
		"the composer still holds it after %d attempts to submit",
		promptSubmitTries,
	)
}

// composerNeedle is the fingerprint of a prompt as the composer would draw it:
// its first line, whitespace-collapsed and clipped. The FIRST line, because a
// multi-line prompt puts every later line below the composer's own marker,
// where this test cannot see it.
func composerNeedle(text string) string {
	first := text
	if index := strings.IndexAny(first, "\r\n"); index >= 0 {
		first = first[:index]
	}
	return naming.ClipRunes(flattenComposerText(first), composerNeedleMax)
}

// composerHolds reports whether the composer — the LAST marker line, below
// every submitted turn Codex keeps on screen — still carries the fingerprint.
func composerHolds(capture, needle string) bool {
	if needle == "" {
		return false
	}
	line := lastLineContaining(capture, codexComposer)
	if line == "" {
		return false
	}
	return strings.Contains(flattenComposerText(line), needle)
}

func lastLineContaining(capture, marker string) string {
	last := ""
	for _, line := range strings.Split(capture, "\n") {
		if strings.Contains(line, marker) {
			last = line
		}
	}
	return last
}

func flattenComposerText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// clearComposer erases text this package typed but will not submit, so an
// abandoned "/rename" never rides along with the user's first real prompt.
//
// One BSpace per typed rune, not C-u: every composer implements backspace,
// while the kill-line binding belongs to the engine's own line editor and a
// build that ignores it would leave the text sitting there — the exact failure
// this function exists to prevent. It reports whether the text is STILL on
// screen afterwards, because a clear nobody verified is a guess.
func clearComposer(
	ctx context.Context,
	tmux Tmux,
	socket, target, typed string,
) bool {
	for range []rune(typed) {
		if err := tmux.SendKey(ctx, socket, target, "BSpace"); err != nil {
			return true
		}
	}
	capture, err := tmux.Capture(ctx, socket, target)
	if err != nil {
		return true
	}
	return strings.Contains(capture, typed)
}

func waitFor(
	ctx context.Context,
	tmux Tmux,
	socket, target, marker string,
	timings Timings,
) bool {
	return pollCapture(ctx, tmux, socket, target, timings, func(capture string) bool {
		return strings.Contains(capture, marker)
	})
}

func pollCapture(
	ctx context.Context,
	tmux Tmux,
	socket, target string,
	timings Timings,
	satisfied func(string) bool,
) bool {
	deadline := clock.Real.Now().Add(timings.Step)
	for {
		capture, err := tmux.Capture(ctx, socket, target)
		if err == nil && satisfied(capture) {
			return true
		}
		if clock.Real.Now().After(deadline) {
			return false
		}
		if err := sleep(ctx, timings.Poll); err != nil {
			return false
		}
	}
}

func sleep(ctx context.Context, duration time.Duration) error {
	return clock.Real.Sleep(ctx, duration)
}

// WindowName reduces a chat name to something tmux can carry as a window name:
// no colons or control characters (both break a target spec), one line, and
// short enough to stay readable in a status bar.
func WindowName(name string) string {
	var builder strings.Builder
	for _, character := range name {
		switch {
		case character == ':' || character == '.':
			builder.WriteByte('-')
		case character < ' ' || character == 0x7f:
			builder.WriteByte(' ')
		default:
			builder.WriteRune(character)
		}
	}
	cleaned := strings.Join(strings.Fields(builder.String()), " ")
	if cleaned == "" {
		return "chat"
	}
	return naming.ClipRunes(cleaned, 40)
}
