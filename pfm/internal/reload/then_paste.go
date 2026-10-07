package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/naming"
)

// A --then follow-up reaches a Codex pane the way internal/spawn delivers a
// launch prompt (spawn.go submitPrompt): ONE bracketed paste, then Enter,
// proven by the composer releasing it. Typed keystrokes do not work there:
// Codex turns every newline of a typed burst into a key of its own, so a
// multi-line follow-up sat in the composer as a draft and was never
// submitted as one message.
const (
	// thenPasteReads bounds the wait for the paste to show in the composer.
	thenPasteReads = 40
	// thenSubmitPresses is how many times Enter is pressed before the
	// follow-up counts as unsubmitted — spawn's promptSubmitTries.
	thenSubmitPresses = 4
	// thenReleaseReads bounds the wait after each Enter for the release.
	thenReleaseReads = 10
	thenPastePoll    = 200 * time.Millisecond
	// thenSubmitPause keeps the Enter from reaching the TUI before it has
	// processed the paste.
	thenSubmitPause = 150 * time.Millisecond
	// thenNeedleMax clips the composer fingerprint — spawn's composerNeedleMax.
	thenNeedleMax = 48
)

var (
	// errThenNotPasted: the composer never showed the pasted follow-up, so
	// no Enter was pressed — a blind Enter would report a send that never
	// landed.
	errThenNotPasted = errors.New("the pasted follow-up never showed in the Codex composer")
	// errThenNotSubmitted: the follow-up was seen in the composer, but no
	// Enter released it, so submission is unproven.
	errThenNotSubmitted = errors.New("the Codex composer never released the follow-up")
)

// deliverCodexThen pastes request.Then into a Codex composer and proves it
// left as one submitted message. A nil options.Paste pastes through tmux's
// own buffer (inject.TmuxInjector.SendPaste) on request.SocketPath.
func deliverCodexThen(
	ctx context.Context,
	request Request,
	options Options,
	tmux Tmux,
	stderr io.Writer,
) error {
	paste := options.Paste
	if paste == nil {
		paste = inject.TmuxInjector{}.SendPaste
	}
	if err := paste(ctx, request.SocketPath, request.Pane, request.Then); err != nil {
		return fmt.Errorf("reload --then: paste prompt into %q: %w", request.Pane, err)
	}
	needle := thenPasteNeedle(request.Then)
	held, err := pollThenComposer(ctx, request, options, tmux, stderr, thenPasteReads, "confirm pasted text",
		func(capture string) bool { return thenPasteHeld(capture, needle) })
	if err != nil {
		return err
	}
	if !held {
		return fmt.Errorf(
			"reload --then: %w — looked for its first line and a paste placeholder on the composer row; refusing blind Enter, the prompt is being saved to %s.then-failed",
			errThenNotPasted,
			filepath.Base(request.SocketPath),
		)
	}
	for press := 1; press <= thenSubmitPresses; press++ {
		if err := options.Clock.Sleep(ctx, thenSubmitPause); err != nil {
			return err
		}
		if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Enter"); err != nil {
			return fmt.Errorf("reload --then: submit prompt (press %d): %w", press, err)
		}
		released, err := pollThenComposer(ctx, request, options, tmux, stderr, thenReleaseReads, "verify prompt submit",
			func(capture string) bool { return thenPasteReleased(capture, needle) })
		if err != nil {
			return err
		}
		if released {
			fmt.Fprintln(stderr, "then: follow-up delivered and submitted")
			return nil
		}
	}
	if err := tmux.Display(
		ctx,
		request.SocketPath,
		request.Pane,
		"reload --then pasted but submit unconfirmed — press Enter",
	); err != nil {
		return fmt.Errorf("reload --then: display unconfirmed submit: %w", err)
	}
	return fmt.Errorf(
		"reload --then: %w — pasted into %q and Enter was pressed %d times; submission is unproven, the prompt is being saved to %s.then-failed",
		errThenNotSubmitted,
		request.Pane,
		thenSubmitPresses,
		filepath.Base(request.SocketPath),
	)
}

// pollThenComposer reads the pane up to reads times until want holds. A
// failed read is reported on stderr and retried, never taken as the answer;
// only a cancelled wait returns an error.
func pollThenComposer(
	ctx context.Context,
	request Request,
	options Options,
	tmux Tmux,
	stderr io.Writer,
	reads int,
	stage string,
	want func(string) bool,
) (bool, error) {
	for read := 1; read <= reads; read++ {
		capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload --then: %s (read %d): %v\n", stage, read, err)
		} else if want(capture) {
			return true, nil
		}
		if err := options.Clock.Sleep(ctx, thenPastePoll); err != nil {
			return false, fmt.Errorf("reload --then: %s: %w", stage, err)
		}
	}
	return false, nil
}

// thenPasteNeedle is the follow-up's fingerprint as the composer row draws
// it: its first line, whitespace-collapsed and clipped. Every later line of
// a multi-line draft sits below the composer row, out of this test's sight.
func thenPasteNeedle(text string) string {
	first := text
	if index := strings.IndexAny(first, "\r\n"); index >= 0 {
		first = first[:index]
	}
	return naming.ClipRunes(strings.Join(strings.Fields(first), " "), thenNeedleMax)
}

// thenPasteHeld reports whether the composer row — the last line carrying
// the composer glyph, below every turn a resumed session redraws — holds the
// follow-up: its fingerprint, or the placeholder a long paste collapses into.
func thenPasteHeld(capture, needle string) bool {
	line := inject.LastComposerLine(capture)
	if line == "" {
		return false
	}
	if inject.HasPastePlaceholder(line) {
		return true
	}
	return needle != "" && strings.Contains(strings.Join(strings.Fields(line), " "), needle)
}

// thenPasteReleased is the submit proof: a composer row is on screen and no
// longer holds the follow-up. A screen with no composer row proves nothing —
// a draft taller than the pane pushes the row off the top.
func thenPasteReleased(capture, needle string) bool {
	return inject.ComposerRowShown(capture) && !thenPasteHeld(capture, needle)
}
