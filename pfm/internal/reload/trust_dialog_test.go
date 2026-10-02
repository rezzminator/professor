package reload

import (
	"context"
	"io"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// trustDialogScreen is Claude Code's folder-trust dialog; its default row is
// "No, exit", so an Enter pressed on it exits the chat.
const trustDialogScreen = " Accessing workspace:\n\n /work/alpha\n\n" +
	" Quick safety check: Is this a project you created or one you trust?\n\n" +
	" ❯ 1. Yes, I trust this folder\n   2. No, exit\n\n Enter to confirm · Esc to cancel\n"

// trustDialogTmux is a reborn pane that came up on the folder-trust dialog and
// records every key sent to it.
type trustDialogTmux struct {
	fakeReloadTmux
	keys []string
}

func (*trustDialogTmux) Capture(context.Context, string, string) (string, error) {
	return trustDialogScreen, nil
}

func (tmux *trustDialogTmux) SendKey(_ context.Context, _, _, key string) error {
	tmux.keys = append(tmux.keys, key)
	return nil
}

func TestDeliverThenRefusesTheClaudeFolderTrustDialogWithoutAKey(t *testing.T) {
	tmux := &trustDialogTmux{}
	err := deliverThen(
		context.Background(),
		Request{
			Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-trust", Pane: "%7",
			Then: "continue the task",
		},
		Options{ThenTries: 2},
		tmux,
		fakeReloadProc{},
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), "folder-trust dialog") {
		t.Fatalf("deliverThen error = %v, want it to name the folder-trust dialog", err)
	}
	if len(tmux.keys) != 0 {
		t.Fatalf("keys sent to the trust dialog: %v", tmux.keys)
	}
}
