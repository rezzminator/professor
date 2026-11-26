package reload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// thenBackstop is a native Claude reload's --then message, stored before its
// /exit and removed once typed: a message the reload could not type reaches
// the reborn chat with its first prompt (ClaimThen, read by the epic-inject
// prompt hook). Socket and Pane name the reload's pane mutex; Session is the
// reborn chat's session id.
type thenBackstop struct {
	Socket  string `json:"socket"`
	Pane    string `json:"pane"`
	Session string `json:"session"`
	Message string `json:"message"`
}

// ThenBackstopPath is a native seat's stored --then message,
// {SIDDir}/reload-herdr-{seat}.then, beside its reload schedule and log.
func ThenBackstopPath(sidDir, seatID string) string {
	return filepath.Join(sidDir, paths.SIDReloadHerdrPrefix+seatID+".then")
}

func storeThen(sidDir, seatID string, backstop thenBackstop) error {
	encoded, err := json.Marshal(backstop)
	if err != nil {
		return fmt.Errorf("encode the reload --then message: %w", err)
	}
	if err := atomicfile.Write(ThenBackstopPath(sidDir, seatID), encoded, 0o600); err != nil {
		return fmt.Errorf("store the reload --then message before /exit: %w", err)
	}
	return nil
}

func dropThen(sidDir, seatID string) error {
	path := ThenBackstopPath(sidDir, seatID)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the stored reload --then message %s: %w", path, err)
	}
	return nil
}

// ClaimThen takes the --then message a native reload of seatID stored and
// could not type, for the prompt of session sessionID: only the reborn chat's
// session takes it, never while the reload still holds the pane (its typed
// --then, or the old chat's /exit, is that prompt), and only once — the claim
// is the file's removal. ok is false when there is nothing to take.
func ClaimThen(sidDir, seatID, sessionID string) (message string, ok bool, err error) {
	if seatID == "" || seatID == "." || seatID == ".." || strings.ContainsAny(seatID, "/\x00") || sessionID == "" {
		return "", false, nil
	}
	path := ThenBackstopPath(sidDir, seatID)
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read the stored reload --then message: %w", err)
	}
	var backstop thenBackstop
	if err := json.Unmarshal(encoded, &backstop); err != nil {
		return "", false, fmt.Errorf("decode the stored reload --then message %s: %w", path, err)
	}
	if backstop.Session != sessionID {
		return "", false, nil
	}
	inFlight, err := InFlight(sidDir, nativeReloadLockKey(backstop.Socket, backstop.Pane), "seat")
	if err != nil || inFlight {
		return "", false, err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("claim the stored reload --then message %s: %w", path, err)
	}
	return backstop.Message, true, nil
}

// then sends the --then turn to the reborn native chat once its composer is
// ready.
func (n *nativeReload) then(ctx context.Context) error {
	if err := n.ready(ctx, false, true); err != nil {
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
// claimSession tells Herdr the pane now runs the reborn conversation. Herdr's
// Claude hook reports a new chat's session with SessionStart source "startup",
// which Herdr ignores on a pane it already tracks Claude in: the pane kept the
// old session, and pfm's tab painter, finding a claim on a session it no
// longer tracks, left the tab on its last paint. Herdr takes the session from
// a "resume" report under its own Claude source. The chat runs either way, so
// a failed claim is a warning.
func (n *nativeReload) claimSession(ctx context.Context, stderr io.Writer) {
	if n.request.Engine != pfmengine.Claude || n.request.SessionID == "" {
		return
	}
	line, err := herdr.SessionReport{
		Pane: n.target.Pane, Source: herdr.ClaudeSource, Agent: herdr.Label(pfmengine.Claude),
		SessionID: n.request.SessionID, StartSource: "resume", Seq: uint64(n.options.Clock.Now().UnixNano()),
	}.Line()
	if err == nil {
		err = herdr.Send(ctx, n.target.SocketPath, line)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: Herdr was not told pane %s now runs session %s, "+
			"so its tab may keep the old chat's state: %v\n", n.target.Pane, n.request.SessionID, err)
	}
}

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
