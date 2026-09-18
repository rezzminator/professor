package inject

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	pfmtmux "hostops/pfm/internal/tmux"
)

var pasteSequence atomic.Uint64

// TmuxInjector invokes tmux against an explicit socket pathname.
type TmuxInjector struct {
	Binary string
}

func (tmux TmuxInjector) Capture(
	ctx context.Context,
	socketPath, target string,
	styled bool,
	scrollback int,
) (string, error) {
	arguments := []string{"capture-pane", "-t", target, "-p", "-J"}
	if styled {
		arguments = append(arguments, "-e")
	}
	switch {
	case scrollback == FullScrollback:
		// chat.sh:1219 — `-S -` starts at the top of the retained buffer, so
		// the whole scrollback is captured, not just the visible fold.
		arguments = append(arguments, "-S", "-")
	case scrollback > 0:
		arguments = append(arguments, "-S", fmt.Sprintf("-%d", scrollback))
	}
	output, err := tmux.command(ctx, socketPath, arguments...).Output()
	return string(output), err
}

func (tmux TmuxInjector) SendLiteral(
	ctx context.Context,
	socketPath, target, text string,
) error {
	return tmux.command(
		ctx,
		socketPath,
		"send-keys",
		"-t",
		target,
		"-l",
		"--",
		text,
	).Run()
}

// SendPaste carries a file-backed message through tmux's paste buffer. The
// -p flag asks tmux to use bracketed-paste mode when the target application
// advertises it; Codex then keeps the full body behind one composer block
// instead of rendering a several-screen inline draft whose submit state
// cannot be proven. A private, one-shot buffer prevents concurrent injects
// into different panes from sharing bytes.
func (tmux TmuxInjector) SendPaste(
	ctx context.Context,
	socketPath, target, text string,
) error {
	buffer := fmt.Sprintf("pfm-inject-%d-%d", os.Getpid(), pasteSequence.Add(1))
	load := tmux.command(ctx, socketPath, "load-buffer", "-b", buffer, "-")
	load.Stdin = strings.NewReader(text)
	if err := load.Run(); err != nil {
		return err
	}
	paste := tmux.command(
		ctx,
		socketPath,
		"paste-buffer",
		"-d",
		"-p",
		"-b",
		buffer,
		"-t",
		target,
	)
	if err := paste.Run(); err != nil {
		_ = tmux.command(ctx, socketPath, "delete-buffer", "-b", buffer).Run()
		return err
	}
	return nil
}

func (tmux TmuxInjector) SendKey(
	ctx context.Context,
	socketPath, target, key string,
) error {
	return tmux.command(
		ctx,
		socketPath,
		"send-keys",
		"-t",
		target,
		key,
	).Run()
}

func (tmux TmuxInjector) CancelCopyMode(
	ctx context.Context,
	socketPath, target string,
) error {
	return tmux.command(
		ctx,
		socketPath,
		"send-keys",
		"-t",
		target,
		"-X",
		"cancel",
	).Run()
}

func (tmux TmuxInjector) PaneInMode(
	ctx context.Context,
	socketPath, target string,
) (bool, error) {
	output, err := tmux.command(
		ctx,
		socketPath,
		"display-message",
		"-t",
		target,
		"-p",
		"#{pane_in_mode}",
	).Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) == "1", nil
}

func (tmux TmuxInjector) PaneCommand(
	ctx context.Context,
	socketPath, target string,
) (string, error) {
	output, err := tmux.command(
		ctx,
		socketPath,
		"display-message",
		"-t",
		target,
		"-p",
		"#{pane_current_command}",
	).Output()
	return strings.TrimSpace(string(output)), err
}

func (tmux TmuxInjector) CurrentSession(
	ctx context.Context,
	socketPath string,
) (string, error) {
	output, err := tmux.command(
		ctx,
		socketPath,
		"display-message",
		"-p",
		"#{session_name}",
	).Output()
	return strings.TrimSpace(string(output)), err
}

// WindowName reads the tmux window name backing a target, the human thread
// name pfm sets on codex panes.
func (tmux TmuxInjector) WindowName(
	ctx context.Context,
	socketPath, target string,
) (string, error) {
	output, err := tmux.command(
		ctx,
		socketPath,
		"display-message",
		"-t",
		target,
		"-p",
		"#{window_name}",
	).Output()
	return strings.TrimSpace(string(output)), err
}

// ClientActivity reports the most recent keystroke time among the clients
// attached to the session that holds target. Verified against a real tmux
// server (jailed probe, Task A.1): `list-clients -t <pane-id>` resolves
// straight through tmux's own cmd-find session->window->pane fallthrough,
// so target is passed exactly as given — a pane id or a session name — with
// no separate session-name resolution step. Zero attached clients is not a
// tmux failure: list-clients still exits 0 and simply prints nothing, which
// is the honest "an unattended pane has no typist" answer (ok=false, err
// nil); a real tmux error is returned as err and must never be read as "the
// pane is unattended".
func (tmux TmuxInjector) ClientActivity(
	ctx context.Context,
	socketPath, target string,
) (time.Time, bool, error) {
	output, err := tmux.command(
		ctx,
		socketPath,
		"list-clients",
		"-t",
		target,
		"-F",
		"#{client_activity}",
	).Output()
	if err != nil {
		return time.Time{}, false, err
	}
	var last time.Time
	found := false
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		seconds, parseErr := strconv.ParseInt(line, 10, 64)
		if parseErr != nil {
			return time.Time{}, false, fmt.Errorf(
				"parse tmux client_activity %q: %w", line, parseErr,
			)
		}
		found = true
		candidate := time.Unix(seconds, 0)
		if candidate.After(last) {
			last = candidate
		}
	}
	return last, found, nil
}

// Display puts a transient notice on the pane's status line — the reload-hold
// pattern (reload.go announcePane; cmd/pfm reloadCommandTmux.Display is the
// real one there): the text travels as ONE argv element, unescaped, exactly
// as that implementation passes it; -d 4000 holds it four seconds so the
// operator has time to read what the waiter is waiting for.
func (tmux TmuxInjector) Display(ctx context.Context, socketPath, target, text string) error {
	return tmux.command(ctx, socketPath, "display-message", "-t", target, "-d", "4000", text).Run()
}

func (tmux TmuxInjector) command(
	ctx context.Context,
	socketPath string,
	arguments ...string,
) *exec.Cmd {
	return pfmtmux.Command(ctx, tmux.Binary, socketPath, arguments...)
}
