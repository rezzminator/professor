package spawn

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// A Codex started in a folder it does not trust has no thread. The markers are
// read from the codex binary's own strings (0.152.1, 0.153.4) and from a live
// 0.159.0 pane:
//
//   - codexNoThread is what Codex answers a thread command (/rename) with when
//     no thread exists; a prompt typed there only queues.
//   - codexFolderTrustTitle and codexFolderTrustYes are 0.159's trust dialog.
//     Its Escape is "Back to Agent Command Center", and a second Escape lands
//     on a Read Only composer with no thread — a screen that looks ready and
//     is not, which is how a chat was reported started with its first prompt
//     still queued. Pressing "Trust and continue" saves a trust decision that
//     lets the folder's settings run code, which pfm leaves to the human.
const (
	codexNoThread         = "No active thread is available."
	codexFolderTrustTitle = "Trust this folder?"
	codexFolderTrustYes   = "Trust and continue"
)

// codexFolderUntrusted reports whether a capture shows that Codex is running —
// or about to run — without a thread because its folder is untrusted. Each
// marker counts only as a whole line, whatever glyph Codex draws before it, and
// never on the composer's own row: a prompt that merely quotes the notice is
// not the notice.
func codexFolderUntrusted(capture string) bool {
	trustAsked, trustOffered := false, false
	for _, line := range strings.Split(capture, "\n") {
		row := strings.TrimSpace(line)
		if strings.HasPrefix(row, codexComposer+" ") && !strings.Contains(row, codexFolderTrustYes) {
			continue
		}
		body := strings.TrimLeftFunc(row, func(r rune) bool { return !unicode.IsLetter(r) })
		switch {
		case body == codexNoThread:
			return true
		case strings.HasPrefix(row, codexFolderTrustTitle):
			trustAsked = true
		case body == codexFolderTrustYes:
			trustOffered = true
		}
	}
	return trustAsked && trustOffered
}

// awaitRenameModal waits for Codex's rename dialog and reports whether it
// opened. Codex saying it has no thread ends the wait at once, as a dialog
// that did not open, instead of waiting out the step budget.
func awaitRenameModal(
	ctx context.Context,
	tmux Tmux,
	socket, target string,
	timings Timings,
) bool {
	opened := false
	pollCapture(ctx, tmux, socket, target, timings, func(capture string) bool {
		opened = renameModalOpen(capture)
		return opened || codexFolderUntrusted(capture)
	})
	return opened
}

// untrustedFolderGuard is the spawn path's check that a Codex chat has a
// thread: run after the rename and after the first prompt, the two doors whose
// keystrokes Codex answers with codexNoThread. A screen that cannot be read is
// an error, never "it has a thread".
func untrustedFolderGuard(
	ctx context.Context,
	tmux Tmux,
	request Request,
	target string,
) error {
	if request.Engine != pfmengine.Codex {
		return nil
	}
	capture, err := tmux.Capture(ctx, request.Socket, target)
	if err != nil {
		return fmt.Errorf(
			"could not read the Codex chat on socket %s to confirm it started a thread: %w",
			request.Socket,
			err,
		)
	}
	if !codexFolderUntrusted(capture) {
		return nil
	}
	return fmt.Errorf(
		"the folder %s is untrusted, so Codex has no active thread: it "+
			"answers %q and the first prompt only queues; trust the folder by "+
			"running codex there yourself, then start the chat again (the "+
			"thread-less chat is still at tmux -L %s attach -t %s)",
		request.CWD,
		codexNoThread,
		request.Socket,
		target,
	)
}
