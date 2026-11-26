package hookentry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// promptClearCommand is `pfm internal command-intercept clear`, the detached
// clear of a typed command command-intercept blocked.
const promptClearCommand = "clear"

// The clear reads the input box every promptClearPoll, at most
// promptClearTries times: Claude Code puts the blocked prompt back only once
// the hook has returned. promptClearIdentify bounds the hook's own share, so
// the clear never holds the typed command's answer.
const (
	promptClearTries    = 50
	promptClearPoll     = 100 * time.Millisecond
	promptClearIdentify = time.Second
)

// startPromptClear starts the clear of the typed command prompt a hook
// blocked; swapped only by this package's tests.
var startPromptClear = func(ctx context.Context, prompt string) error {
	ctx, cancel := context.WithTimeout(ctx, promptClearIdentify)
	defer cancel()
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
	if err != nil {
		return fmt.Errorf("identify the chat: %w", err)
	}
	identity, err := identifier.Identify(ctx)
	if err != nil {
		return fmt.Errorf("identify the chat: %w", err)
	}
	if identity.SocketPath == "" || identity.Pane == "" {
		return fmt.Errorf("identify the chat: no seat socket or pane (socket %q, pane %q)",
			identity.SocketPath, identity.Pane)
	}
	// The seat id, not the pane, names a Herdr chat: a /pfm:space move gives
	// its pane a new id, which the clear's capture proves from the seat.
	return startDetached([]string{
		internalCommand, "command-intercept", promptClearCommand, "--backend", identity.Backend,
		"--socket", identity.SocketPath, "--pane", identity.Pane, "--seat", identity.SeatID, "--", prompt,
	})
}

// blockTypedCommand blocks a typed command's prompt with reason and clears
// the command out of the input box Claude Code puts it back into (measured on
// 2.1.331), so the box holds no leftover once the command has run; a
// scheduled reload never asks, as it stashes the box's draft itself
// (reload/ready.go). A clear that cannot start is logged; the block stands.
func blockTypedCommand(ctx context.Context, stdout io.Writer, prompt, reason string) int {
	if err := startPromptClear(ctx, prompt); err != nil {
		recordTypedCommand("prompt.clear_failed", prompt, "start the clear: "+err.Error())
	}
	return blockPrompt(stdout, reason)
}

// PromptClear is `pfm internal command-intercept clear --backend B --socket S --pane P
// [--seat ID] -- prompt`: it clears the chat's input box once the box holds
// exactly prompt (clearTypedPrompt). It runs detached, so a failure is logged
// as prompt.clear_failed, never printed.
func PromptClear(args []string, stderr io.Writer) int {
	flags := cli.NewFlagSet(
		"internal command-intercept "+promptClearCommand,
		"usage: pfm internal command-intercept clear --backend tmux|herdr --socket path --pane id [--seat id] -- prompt",
		stderr,
	)
	var target pfmcall.Target
	flags.StringVar(&target.Backend, "backend", "", "the chat's seat backend (empty means tmux)")
	flags.StringVar(&target.SocketPath, "socket", "", "socket path of the chat's seat")
	flags.StringVar(&target.Pane, "pane", "", "the chat's pane id")
	flags.StringVar(&target.SeatID, "seat", "", "the chat's Herdr seat id")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 || target.SocketPath == "" || target.Pane == "" {
		flags.Usage()
		return 2
	}
	prompt := flags.Arg(0)
	screen, err := pfmcall.New(pfmcall.Dependencies{WarningWriter: io.Discard})
	if err == nil {
		wait := func(ctx context.Context) error { return clock.Real.Sleep(ctx, promptClearPoll) }
		_, err = clearTypedPrompt(context.Background(), screen, target, prompt, promptClearTries, wait)
	}
	if err != nil {
		recordTypedCommand("prompt.clear_failed", prompt, err.Error())
		return 1
	}
	return 0
}

// clearTypedPrompt empties target's input box once it holds exactly prompt,
// spaces folded as ComposerDraftText folds them: C-e to the line end, then C-u deleting the
// line before the cursor (measured on Claude Code 2.1.331). A box holding
// anything else is never touched, so text typed since is kept; it reads the
// box at most tries times, wait between reads, and reports whether it
// cleared. A read that fails is retried, as a /pfm:space move re-addresses
// the pane while it runs; the last failure is returned when no read matched.
// shortcut: a prompt wrapping onto a second row never matches the box's last
// row, so it stays; read ComposerBoxText's rows if long commands must clear.
func clearTypedPrompt(
	ctx context.Context,
	screen pfmcall.Screen,
	target pfmcall.Target,
	prompt string,
	tries int,
	wait func(context.Context) error,
) (bool, error) {
	want := strings.Join(strings.Fields(prompt), " ")
	var failed error
	for try := 0; try < tries; try++ {
		if try > 0 {
			if err := wait(ctx); err != nil {
				return false, errors.Join(failed, err)
			}
		}
		capture, err := screen.CaptureScreen(ctx, target)
		if err != nil {
			failed = fmt.Errorf("read the input box of pane %s: %w", target.Pane, err)
			continue
		}
		if want == "" || pfmcall.ComposerDraftText(capture) != want {
			continue
		}
		// shortcut: text typed between this read and the keys is cleared with
		// the command; a few milliseconds, as for every key pfm sends a box.
		for _, key := range []string{"C-e", "C-u"} {
			if err := screen.SendKey(ctx, target, key); err != nil {
				return false, fmt.Errorf("clear %q from the input box of pane %s: %w", want, target.Pane, err)
			}
		}
		return true, nil
	}
	return false, failed
}
