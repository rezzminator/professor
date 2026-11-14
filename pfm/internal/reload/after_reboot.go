package reload

import (
	"context"
	"fmt"
	"io"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// then sends the --then turn to the reborn native chat once its composer is
// ready.
func (n *nativeReload) then(ctx context.Context) error {
	if err := n.ready(ctx, false); err != nil {
		return fmt.Errorf("native reload --then: %w", err)
	}
	if err := n.refreshTarget(ctx); err != nil {
		return err
	}
	result, err := n.door.CallTarget(ctx, n.target, pfmcall.Request{Message: n.request.Then})
	if err != nil {
		return fmt.Errorf("native reload --then: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("native reload --then unconfirmed: %s", result.Message)
	}
	return nil
}

// labelLeftBehind relabels the Claude conversation a native `--new` reboot
// abandoned, when it carries a custom title: the reborn seat starts under the
// same name, and two rows of one name sent every by-name verb to the dead one.
// A title-less transcript is named from its prompts and has nothing to carry.
func (n *nativeReload) labelLeftBehind(session string, stderr io.Writer) {
	if !n.request.fresh || n.request.Engine != pfmengine.Claude || session == "" {
		return
	}
	transcript, err := findClaudeTranscript(n.options.ClaudeRoots, session)
	if err == nil && transcript == "" {
		return
	}
	title := ""
	if err == nil {
		title, err = TranscriptTitle(transcript)
	}
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm chat reload --new: the conversation left behind (%s) is NOT relabelled: %v\n",
			session,
			err,
		)
		return
	}
	labelLeftBehind(transcript, title, n.options, stderr)
}

// labelLeftBehind names the conversation a `--new` reboot abandoned
// "<name> (before --new)" in its transcript, so no by-name verb reaches it in
// place of the live chat — the tmux path (followName) and the native Herdr
// path (RunHerdr) both end here. A failure is a warning: the reboot it
// follows already succeeded.
func labelLeftBehind(transcript, name string, options Options, stderr io.Writer) {
	if transcript == "" || name == "" {
		return
	}
	title := name + LeftBehindSuffix
	if err := labelTranscript(transcript, title, TranscriptBackupDir(options.Home), options.Clock); err != nil {
		fmt.Fprintf(
			stderr,
			"pfm chat reload --new: the conversation left behind in %s is NOT relabelled %q: %v\n",
			transcript,
			title,
			err,
		)
		return
	}
	fmt.Fprintf(
		stderr,
		"pfm chat reload --new: conversation left behind relabelled %q (%s)\n",
		title,
		transcript,
	)
}
