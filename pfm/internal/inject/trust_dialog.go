package inject

import (
	"context"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// refuseTrustDialog turns base into the refusal for a pane held at Claude Code's
// folder-trust dialog. Its default row is "No, exit" and trusting a folder is
// the human's decision, so nothing is typed or pressed — not even a force-now
// Escape.
func refuseTrustDialog(base Result, pane, capture string) (Result, bool) {
	if !pfmengine.ClaudeTrustDialog(capture) {
		return base, false
	}
	base.Code = CodeUndelivered
	base.Message = fmt.Sprintf(
		"ABORT: %q is held at Claude Code's folder-trust dialog; nothing was typed or pressed "+
			"(attach it and choose \"Yes, I trust this folder\" yourself)",
		pane,
	)
	return base, true
}

// RescueOutcome is what RescueLaunchPrompt did to the pane.
type RescueOutcome int

const (
	// RescueNotSent: no retry was completed — the pane could not be read, or a
	// key failed to reach tmux.
	RescueNotSent RescueOutcome = iota
	// RescueKeysPressed: the dismiss and the Enter were both delivered.
	RescueKeysPressed
	// RescueTrustHeld: the pane is Claude Code's folder-trust dialog; nothing
	// was pressed, because its default row is "No, exit".
	RescueTrustHeld
)

// RescueLaunchPrompt presses the keys a human presses when a launch prompt is
// sitting typed-but-unsent: Escape to clear the startup overlay that swallowed
// the submit, settle, then Enter. The pane is read first, because Claude Code's
// folder-trust dialog takes both keys as "No, exit"; it is reported and left
// alone, and a pane that cannot be read gets no key either.
func RescueLaunchPrompt(
	ctx context.Context,
	tmux Tmux,
	socketPath, pane string,
	clk clock.Clock,
	settle time.Duration,
) (RescueOutcome, error) {
	capture, err := tmux.Capture(ctx, socketPath, pane, false, 0)
	if err != nil {
		return RescueNotSent, fmt.Errorf("read %s before the retry: %w", pane, err)
	}
	if pfmengine.ClaudeTrustDialog(capture) {
		return RescueTrustHeld, nil
	}
	if err := tmux.SendKey(ctx, socketPath, pane, "Escape"); err != nil {
		return RescueNotSent, fmt.Errorf("press Escape on %s: %w", pane, err)
	}
	if err := clk.Sleep(ctx, settle); err != nil {
		return RescueNotSent, err
	}
	if err := tmux.SendKey(ctx, socketPath, pane, "Enter"); err != nil {
		return RescueNotSent, fmt.Errorf("press Enter on %s: %w", pane, err)
	}
	return RescueKeysPressed, nil
}
