package spawn

import (
	"context"
	"strings"
	"testing"
)

// trustDialogScreen is Claude Code's folder-trust dialog as it draws on a
// fresh folder: its default row is "No, exit", so an Enter or Escape sent to
// it ends the chat.
const trustDialogScreen = " Accessing workspace:\n\n /work/alpha\n\n" +
	" Quick safety check: Is this a project you created or one you trust?\n\n" +
	" ❯ 1. Yes, I trust this folder\n   2. No, exit\n\n Enter to confirm · Esc to cancel\n"

// trustPane is a Claude pane stuck on the folder-trust dialog.
type trustPane struct {
	*fakeCodex
}

func (trustPane) Capture(context.Context, string, string) (string, error) {
	return trustDialogScreen, nil
}

func TestRunHoldsAtTheClaudeFolderTrustDialogWithoutAKey(t *testing.T) {
	pane := trustPane{newFakeCodex()}
	result, err := Run(context.Background(), pane, Request{
		Engine:  "cc",
		Name:    "worker",
		Socket:  "cc-1-2-3",
		CWD:     "/work/alpha",
		Run:     "claude --name worker",
		Prompt:  "audit the firewall",
		Timings: testTimings(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.TrustHeld {
		t.Fatalf("result = %#v, want TrustHeld", result)
	}
	if len(pane.keys) != 0 {
		t.Fatalf("keys sent to the trust dialog: %v", pane.keys)
	}
	if result.Prompted || strings.Join(result.Warnings, "") != "" {
		t.Fatalf("result = %#v, want nothing prompted and no warning", result)
	}
}

func TestTrustRefusalNamesTheDialogTheFolderAndTheAttachLine(t *testing.T) {
	result := Result{Socket: "cc-1-2-3", Session: "cc-1-2-3"}
	for directory, want := range map[string]string{
		"/work/alpha": "worker is held at Claude Code's folder-trust dialog for /work/alpha — pfm pressed nothing",
		"":            "worker is held at Claude Code's folder-trust dialog — pfm pressed nothing",
	} {
		got := result.TrustRefusal("worker", directory)
		if !strings.HasPrefix(got, want) ||
			!strings.Contains(got, "tmux -L cc-1-2-3 attach -t cc-1-2-3") ||
			!strings.Contains(got, `choose "Yes, I trust this folder"`) {
			t.Fatalf("TrustRefusal(%q) = %q, want prefix %q", directory, got, want)
		}
	}
}
