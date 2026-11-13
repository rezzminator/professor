package call

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// Screen is the one door through which code outside this package presses keys
// in, types into and reads a live chat's pane. *Engine is it: the target's own
// address (tmux socket and pane, or a proved Herdr seat) decides the host,
// once, here — kill, reload, status and spawn never pick a host's verb for
// themselves. Tests hand their own Screen in.
type Screen interface {
	SendKey(ctx context.Context, target Target, key string) error
	SendLiteral(ctx context.Context, target Target, text string) error
	// SendPaste types text as one bracketed paste: byte-safe at any size, and
	// never read by the engine as a burst of typed keys.
	SendPaste(ctx context.Context, target Target, text string) error
	CaptureScreen(ctx context.Context, target Target) (string, error)
	CaptureStyled(ctx context.Context, target Target) (string, error)
	// CaptureTarget reads the screen plus up to tailLines of history
	// (FullScrollback: all of it); 0 is the visible screen.
	CaptureTarget(ctx context.Context, target Target, tailLines int) (string, error)
	// LeaveCopyMode takes a tmux pane out of copy mode, reporting whether it
	// was in it. A mode read that fails is an error, never "not in copy mode":
	// keys pressed into copy mode move its cursor, not the chat's composer.
	LeaveCopyMode(ctx context.Context, target Target) (bool, error)
}

var _ Screen = (*Engine)(nil)

// SendPaste delivers text to the explicitly resolved seat as one paste.
func (engine *Engine) SendPaste(ctx context.Context, target Target, text string) error {
	switch target.Backend {
	case "", backendTmux:
		return engine.tmux.SendPaste(ctx, target.SocketPath, target.Pane, text)
	case backendHerdr:
		_, err := herdrSendText(ctx, target, text, "pane.send_input")
		return err
	default:
		return fmt.Errorf("send paste: unsupported seat backend %q", target.Backend)
	}
}

// LeaveCopyMode is the one copy-mode exit: tmux's pane_in_mode, then its
// cancel. A Herdr pane has no copy mode.
func (engine *Engine) LeaveCopyMode(ctx context.Context, target Target) (bool, error) {
	switch target.Backend {
	case "", backendTmux:
		inMode, err := engine.tmux.PaneInMode(ctx, target.SocketPath, target.Pane)
		if err != nil {
			return false, fmt.Errorf("read whether pane %s is in copy mode: %w", target.Pane, err)
		}
		if !inMode {
			return false, nil
		}
		if err := engine.tmux.CancelCopyMode(ctx, target.SocketPath, target.Pane); err != nil {
			return false, fmt.Errorf("leave copy mode in pane %s: %w", target.Pane, err)
		}
		return true, nil
	case backendHerdr:
		return false, nil
	default:
		return false, fmt.Errorf("leave copy mode: unsupported seat backend %q", target.Backend)
	}
}

// CaptureScreen reads the target's visible frame, plain, with no history — the
// frame footer, composer and dialog checks read.
func (engine *Engine) CaptureScreen(ctx context.Context, target Target) (string, error) {
	return engine.captureVisible(ctx, target, false)
}

// CaptureStyled is CaptureScreen with the styling kept: the dim SGR that tells
// a composer's suggestion from a typed draft.
func (engine *Engine) CaptureStyled(ctx context.Context, target Target) (string, error) {
	return engine.captureVisible(ctx, target, true)
}

func (engine *Engine) captureVisible(ctx context.Context, target Target, styled bool) (string, error) {
	switch target.Backend {
	case "", backendTmux:
		screen, err := engine.tmux.Capture(ctx, target.SocketPath, target.Pane, styled, 0)
		if err != nil {
			if gather.TmuxServerGone(err) {
				err = fmt.Errorf("%w: %w", gather.ErrServerGone, err) // the pane's chat ended
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) && strings.TrimSpace(string(exit.Stderr)) != "" {
				return "", fmt.Errorf(
					"capture pane %s: %w: %s",
					target.Pane,
					err,
					strings.TrimSpace(string(exit.Stderr)),
				)
			}
			return "", fmt.Errorf("capture pane %s: %w", target.Pane, err)
		}
		return screen, nil
	case backendHerdr:
		format := ""
		if styled {
			format = "ansi"
		}
		return herdrRawCapture(ctx, target, format)
	default:
		return "", fmt.Errorf("capture: unsupported seat backend %q", target.Backend)
	}
}
