package chat

import (
	"context"
	"fmt"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/inject"
)

// PaneCapture reads one live chat's pane. It is the seam Status crosses to
// answer working-vs-idle for a chat whose transcript cannot answer it; nil
// selects the real tmux capture.
type PaneCapture func(ctx context.Context, socketPath, target string) (string, error)

// PaneTarget is the ONE ladder from a resolved chat to the tmux target its
// pane is addressed by: the pane id, else the session name, else the socket
// itself. Shared by `pfm chat capture` and Status's own capture, because two
// copies is exactly how one of them ends up reading somebody else's pane.
func PaneTarget(chat headless.Chat) string {
	if chat.Pane != "" {
		return chat.Pane
	}
	if chat.Session != "" {
		return chat.Session
	}
	return chat.Socket
}

// needsPaneState reports whether Inspect's verdict for this chat rests on no
// transcript evidence at all: a live seat whose engine writes no transcript
// this process can read (OpenCode), or one whose transcript exists but holds
// no turn yet (a chat still sitting at an empty prompt). headless.Inspect
// answers StateWorking for both — the honest answer given only a file, and a
// wrong one about a chat that is plainly waiting for its human.
func needsPaneState(chat headless.Chat, status headless.Status) bool {
	return chat.Live && (chat.Path == "" || status.Last == "")
}

// paneState is the pane-evidence verdict: the engine's own running-turn
// footer, read by that engine's own rule.
func paneState(engine pfmengine.ID, capture string) string {
	if inject.IsBusyFor(engine, capture) {
		return headless.StateWorking
	}
	return headless.StateIdle
}

// statusFromPane replaces a transcript-less live chat's state with what its
// own screen says.
//
// A capture that could not RUN comes back as an ERROR and never as a state:
// "we failed to look" rendered as "the chat is idle" is the exact confusion
// headless refuses everywhere else.
func statusFromPane(
	ctx context.Context,
	chat headless.Chat,
	status headless.Status,
	capture PaneCapture,
	runtime *pfmconfig.Runtime,
) (headless.Status, error) {
	if capture == nil {
		capture = func(ctx context.Context, socketPath, target string) (string, error) {
			return (inject.TmuxInjector{}).Capture(ctx, socketPath, target, false, inject.FullScrollback)
		}
	}
	values, err := NameResolver{Runtime: runtime}.paths()
	if err != nil {
		return status, fmt.Errorf("resolve %s socket directory for a pane read: %w", chat.Name, err)
	}
	socketPath, err := values.SocketUnder(chat.Socket)
	if err != nil {
		return status, fmt.Errorf("resolve %s socket %q: %w", chat.Name, chat.Socket, err)
	}
	screen, err := capture(ctx, socketPath, PaneTarget(chat))
	if err != nil {
		return status, fmt.Errorf("read %s pane for status: %w", chat.Name, err)
	}
	status.State = paneState(chat.Engine, screen)
	// Inspect derives IdleSeconds from the transcript's mtime, which is the
	// very evidence this path exists because there is none of.
	status.IdleSeconds = 0
	return status, nil
}
