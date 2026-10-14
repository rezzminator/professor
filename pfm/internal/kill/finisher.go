package kill

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	defaultExitDelay    = 1500 * time.Millisecond
	defaultPollEvery    = time.Second
	defaultPollAttempts = 20
	// defaultIdleAttempts bounds the wait for the chat's running turn to end
	// before the exit command is typed: the caller of a self-exit was told to
	// reply one line and end its turn.
	defaultIdleAttempts = 20
	// bunkerSocket is the one tmux server that hosts terminal tabs rather than
	// chats (vsct.sh), so it is the only place a chat's viewport can be a pane.
	bunkerSocket = "vsct"
)

type indexRefresher struct {
	database    *store.Store
	claudeRoots []string
	codexHomes  []string
}

func (refresher indexRefresher) Refresh(ctx context.Context) error {
	indexer, err := index.NewWithRoots(refresher.database, paths.Values{}, map[pfmengine.ID][]string{
		pfmengine.Claude: refresher.claudeRoots,
		pfmengine.Codex:  refresher.codexHomes,
	})
	if err != nil {
		return err
	}
	_, err = indexer.Run(ctx, index.Options{})
	return err
}

// NewFinisher constructs the killed internal subcommand worker.
func NewFinisher(
	database *store.Store,
	dependencies Dependencies,
) (*Finisher, error) {
	if database == nil {
		return nil, errors.New("kill finisher store is nil")
	}
	resolved := dependencies.Paths
	if resolved.Home == "" {
		var err error
		resolved, err = paths.Resolve()
		if err != nil {
			return nil, fmt.Errorf("resolve kill finisher paths: %w", err)
		}
	}
	tmux := dependencies.Tmux
	if tmux == nil {
		tmux = TmuxKiller{}
	}
	now := dependencies.Now
	if now == nil {
		now = clock.Real.Now
	}
	codexHomes := dependencies.CodexHomes
	if codexHomes == nil {
		codexHomes = append([]string(nil), resolved.Roots[pfmengine.Codex]...)
	} else {
		codexHomes = append([]string{}, codexHomes...)
	}
	refresher := dependencies.Refresher
	if refresher == nil {
		claudeRoots := dependencies.ClaudeRoots
		if len(claudeRoots) == 0 {
			claudeRoots = resolved.Roots[pfmengine.Claude]
		}
		refresher = indexRefresher{
			database:    database,
			claudeRoots: append([]string(nil), claudeRoots...),
			codexHomes:  append([]string(nil), codexHomes...),
		}
	}
	delay := dependencies.Delay
	if delay == 0 {
		delay = defaultExitDelay
	}
	pollEvery := dependencies.PollEvery
	if pollEvery == 0 {
		pollEvery = defaultPollEvery
	}
	pollAttempts := dependencies.PollAttempts
	if pollAttempts == 0 {
		pollAttempts = defaultPollAttempts
	}
	idleAttempts := dependencies.IdleAttempts
	if idleAttempts == 0 {
		idleAttempts = defaultIdleAttempts
	}
	log := dependencies.Log
	if log == nil {
		log = os.Stderr
	}
	return &Finisher{
		idleAttempts: idleAttempts,
		log:          log,
		database:     database,
		tmux:         tmux,
		refresher:    refresher,
		now:          now,
		delay:        delay,
		pollEvery:    pollEvery,
		pollAttempts: pollAttempts,
		paths: resolvedPaths{
			home:       resolved.Home,
			sidDir:     resolved.SIDDir,
			codexHomes: codexHomes,
			tmuxDir:    resolved.TmuxDir,
		},
	}, nil
}

// exitCommands is the set of engines this finisher knows how to close, each
// with the exit command it types into a composer it can prove empty. "" is an
// engine with no provable-empty composer, which therefore never gets one and
// closes by the fallback kill alone — an intended outcome, since Enter on a
// draft sends it as a prompt and the chat is ending hidden either way. Codex
// draws its › row with no box beneath (a draft that wraps or starts with a
// newline has no known end); pfm reads no OpenCode composer at all.
var exitCommands = map[pfmengine.ID]string{
	pfmengine.Claude:   "/exit",
	pfmengine.Codex:    "",
	pfmengine.OpenCode: "",
}

// Run performs the delayed graceful close, fallback kill, cleanup, refresh,
// post-exit kill, and teammate reap.
func (finisher *Finisher) Run(
	ctx context.Context,
	args ExitArgs,
) error {
	command, known := exitCommands[args.Engine]
	if !known {
		return fmt.Errorf("unknown kill-exit engine %q", args.Engine)
	}
	if args.ID == "" || args.SocketPath == "" || args.PaneID == "" {
		return errors.New("kill-exit requires id, socket, and pane")
	}
	if err := waitContext(ctx, finisher.delay); err != nil {
		return err
	}
	frame, turn, err := finisher.turnEnded(ctx, args)
	if err != nil {
		return err
	}

	// Read the viewports BEFORE the chat dies: its clients vanish with its server,
	// and they are the only evidence of which panes were watching it.
	viewports := finisher.viewportPanes(ctx, args.SocketPath)

	// The exit command goes only into a composer proven empty once the turn
	// is over; every other run types nothing, and its one log line names why
	// — the detached finisher's stderr is the chat's kill-exit log, so the
	// reason is never absence. The fallback kill ends the pane either way.
	landed := false
	probeErr := finisher.exitRefusal(ctx, args, command, frame, turn)
	if probeErr == nil {
		_ = finisher.tmux.SendLine(ctx, args.SocketPath, args.PaneID, command)
		landed, probeErr, err = pollPaneGone(
			ctx, finisher.tmux, args.SocketPath, args.PaneID, finisher.pollAttempts, finisher.pollEvery,
			finisher.answerExitDialog(args),
		)
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(
			finisher.log,
			"pfm internal kill-exit %s: no exit command typed — %v; the fallback kill-pane closes the pane\n",
			args.ID, probeErr,
		)
	}
	// The fallback kill always runs, matching the graceful-close-then-
	// fallback-kill choreography regardless of what the grace window saw —
	// but its error only MATTERS when the loop itself never confirmed the
	// pane gone: against an already-confirmed-gone pane a forced kill is
	// expected to fail ("can't find pane") and that is not itself evidence
	// of anything. A kill the finisher has not seen land either way is
	// never the one it records.
	// A failed kill of a pane already gone (it died before the turn ended)
	// is no evidence either way, so the pane is asked once more.
	if killErr := finisher.tmux.KillPane(ctx, args.SocketPath, args.PaneID); killErr == nil {
		landed = true
	} else if !landed {
		exists, existsErr := finisher.tmux.PaneExists(ctx, args.SocketPath, args.PaneID)
		if existsErr == nil && !exists {
			landed = true
		} else {
			probeErr = errors.Join(probeErr, killErr, existsErr)
		}
	}
	finisher.closeViewports(ctx, viewports)

	var cleanupErrors []error
	for _, crumb := range []string{
		filepath.Join(finisher.paths.sidDir, args.SocketName+"."+args.PaneID),
		filepath.Join(finisher.paths.sidDir, args.SocketName),
	} {
		if err := os.Remove(crumb); err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}

	if err := finisher.refresher.Refresh(ctx); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("refresh post-exit index: %w", err))
	} else if landed {
		if err := finisher.recordPostExitKill(ctx, args); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	} else {
		cleanupErrors = append(cleanupErrors, fmt.Errorf(
			"kill-exit for %s: pane %s was never confirmed gone: %w",
			args.ID, args.PaneID, probeErr,
		))
	}
	if err := finisher.reapTeammates(ctx, args.ID); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return errors.Join(cleanupErrors...)
}

// turnState is what turnEnded saw: the turn over, still running at the
// bound, or the pane already gone.
type turnState int

const (
	turnRunning turnState = iota
	turnOver
	turnPaneGone
)

// turnEnded waits, idleAttempts polls at most, for the pane's footer to read
// idle on two captures in a row — reload's worker's rule (waitCallerIdle): an
// exit command typed into a running turn is conversation text, never a TUI
// command, and a self-exit's caller was told to reply one line and end its
// turn. A failed capture is no reading and resets the count, unless the pane
// is already gone, which needs no wait. turnRunning at the bound. The frame
// is the last idle capture, the one the composer is read from.
func (finisher *Finisher) turnEnded(ctx context.Context, args ExitArgs) (string, turnState, error) {
	quiet := 0
	for attempt := 0; attempt < finisher.idleAttempts; attempt++ {
		capture, err := finisher.tmux.Capture(ctx, args.SocketPath, args.PaneID)
		switch {
		case err == nil && !pfmcall.IsFooterBusy(args.Engine, capture):
			quiet++
			if quiet >= 2 {
				return capture, turnOver, nil
			}
		case err != nil:
			quiet = 0
			exists, probeErr := finisher.tmux.PaneExists(ctx, args.SocketPath, args.PaneID)
			if probeErr == nil && !exists {
				return "", turnPaneGone, nil
			}
		default:
			quiet = 0
		}
		if attempt < finisher.idleAttempts-1 {
			if err := waitContext(ctx, finisher.pollEvery); err != nil {
				return "", turnRunning, err
			}
		}
	}
	return "", turnRunning, nil
}

// exitRefusal is nil only when the exit command may be typed: the turn over,
// the engine one with a provable-empty composer (a non-empty command), and
// the turn's last idle frame proving the composer empty — reload's rule
// (composerEmpty), read by inject: the box drawn, no selector open on it, no
// draft or only a dim suggestion on the marker row, and no text on any row down to the box's
// closing rule. Anything else is the reason nothing is typed.
func (finisher *Finisher) exitRefusal(
	ctx context.Context,
	args ExitArgs,
	command, frame string,
	turn turnState,
) error {
	switch {
	case turn == turnRunning:
		return errors.New("the turn was still running at the bound")
	case turn == turnPaneGone:
		return fmt.Errorf("pane %s was already gone when the turn ended", args.PaneID)
	case command == "":
		return fmt.Errorf("pfm cannot prove the %s composer empty", pfmengine.MustLookup(args.Engine).Short)
	case pfmcall.LastComposerLine(frame) == "":
		return fmt.Errorf("pane %s drew no composer", args.PaneID)
	case pfmcall.SelectorLine(frame) != "":
		// A permission prompt or menu parks the turn with an idle footer;
		// its ❯ row is a selection, not a human's draft.
		return fmt.Errorf("pane %s shows the open selector %q", args.PaneID, pfmcall.SelectorLine(frame))
	}
	empty, draft, err := pfmcall.ComposerEmpty(frame, func() (string, error) {
		return finisher.tmux.CaptureStyled(ctx, args.SocketPath, args.PaneID)
	})
	switch {
	case err != nil:
		return fmt.Errorf("pane %s: %w", args.PaneID, err)
	case !empty:
		return fmt.Errorf("pane %s holds the draft %q", args.PaneID, draft)
	case !pfmcall.ComposerBoxEmpty(frame):
		return fmt.Errorf(
			"pane %s: the composer box holds text below its marker row or has no closing rule",
			args.PaneID,
		)
	}
	return nil
}

// answerExitDialog is the grace window's per-poll step while the pane lives:
// /exit over background work (a background shell, a scheduled task, a
// sub-agent) opens Claude Code's confirmation with "Exit and stop tasks"
// preselected, and the chat exits only once Enter answers it — reload's
// worker presses the same key on the same dialog. The background work stops
// with the chat either way.
func (finisher *Finisher) answerExitDialog(args ExitArgs) func(context.Context) error {
	return func(ctx context.Context) error {
		capture, err := finisher.tmux.Capture(ctx, args.SocketPath, args.PaneID)
		if err != nil {
			return fmt.Errorf("read pane %s for an exit dialog: %w", args.PaneID, err)
		}
		if !pfmcall.ExitDialogOpen(capture) {
			return nil
		}
		if err := finisher.tmux.SendKey(ctx, args.SocketPath, args.PaneID, "Enter"); err != nil {
			return fmt.Errorf("confirm the exit dialog in pane %s: %w", args.PaneID, err)
		}
		return nil
	}
}

// viewportPanes returns the bunker panes this chat is being watched through.
//
// The join is the TTY: a viewport client RUNS IN a bunker pane, so the client's
// terminal and the pane's terminal are the same device. That holds for both
// shapes a viewport comes in — the pane whose shell was EXEC'd into the client,
// and the pane whose shell merely spawned it as a child — which is why the tty
// is used instead of inspecting processes. Only the second shape needs closing:
// the exec'd pane dies with the client on its own. Any failure returns nothing,
// because a chat that closes without taking its tab is far better than a kill
// that dies trying.
func (finisher *Finisher) viewportPanes(
	ctx context.Context,
	chatSocketPath string,
) []string {
	ttys, err := finisher.tmux.ClientTTYs(ctx, chatSocketPath)
	if err != nil || len(ttys) == 0 {
		return nil
	}
	bunkerPath := filepath.Join(finisher.paths.tmuxDir, bunkerSocket)
	panes, err := finisher.tmux.PanesByTTY(ctx, bunkerPath)
	if err != nil || len(panes) == 0 {
		return nil
	}
	var viewports []string
	for _, tty := range ttys {
		if paneID, watching := panes[tty]; watching {
			viewports = append(viewports, paneID)
		}
	}
	return viewports
}

// closeViewports takes down the panes that existed only to watch the chat that
// just ended. A pane the client's exit already closed is simply not there, and
// killing the last pane of a bunker ends its session — which is what closes the
// terminal tab.
func (finisher *Finisher) closeViewports(ctx context.Context, viewports []string) {
	if len(viewports) == 0 {
		return
	}
	bunkerPath := filepath.Join(finisher.paths.tmuxDir, bunkerSocket)
	for _, paneID := range viewports {
		_ = finisher.tmux.KillPane(ctx, bunkerPath, paneID)
	}
}

// recordPostExitKill makes a standing kill permanent after the post-exit index
// refresh, without undoing an unkill that landed during the refresh.
func (finisher *Finisher) recordPostExitKill(
	ctx context.Context,
	args ExitArgs,
) error {
	if _, err := finisher.database.ReassertKill(ctx, args.ID); err != nil {
		return fmt.Errorf("record post-exit kill: %w", err)
	}
	return nil
}

// reapTeammates takes down the chats this chat spawned, reading detached and
// shared-pane teammates from the shared store. The flat files under
// ~/.claude/.cc-{new,pane}-children are the fallback the children helper
// describes; the live fleet stopped writing them, and reading them alone is why
// teammates outlived their orchestrator.
//
// A detached teammate owns its socket, so it dies by kill-server. A pane
// teammate shares this chat's server, so it dies by kill-pane and never by
// kill-server, which would take this chat's neighbours down with it
// (chat.sh:428-431).
func (finisher *Finisher) reapTeammates(
	ctx context.Context,
	id string,
) error {
	var reapErrors []error
	for _, kind := range []string{fleetdb.KindNew, fleetdb.KindPane} {
		flatPath := filepath.Join(finisher.paths.home, ".claude", ".cc-"+kind+"-children", id)
		values, err := finisher.children(ctx, kind, id, flatPath)
		if err != nil {
			reapErrors = append(reapErrors, err)
			continue
		}
		var retained []string
		for _, value := range values {
			err := finisher.reapChild(ctx, kind, value)
			if err == nil {
				err = finisher.database.Shared().ClearChild(ctx, kind, id, value)
			}
			if err != nil {
				reapErrors = append(reapErrors, fmt.Errorf("reap %s child %q of %s: %w", kind, value, id, err))
				retained = append(retained, value)
			}
		}
		if len(retained) == 0 {
			err = os.Remove(flatPath)
		} else if _, statErr := os.Stat(flatPath); statErr == nil {
			err = atomicfile.Write(flatPath, []byte(strings.Join(retained, "\n")+"\n"), 0o600)
		} else {
			err = statErr
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			reapErrors = append(reapErrors, fmt.Errorf("update children file %s: %w", flatPath, err))
		}
	}
	return errors.Join(reapErrors...)
}

func (finisher *Finisher) reapChild(ctx context.Context, kind, value string) error {
	socket, pane, found := strings.Cut(value, "\t")
	if kind == fleetdb.KindNew {
		socket = strings.TrimSpace(value)
	} else if !found || pane == "" {
		return fmt.Errorf("invalid pane registration %q", value)
	}
	if socket == "" {
		return fmt.Errorf("empty socket registration %q", value)
	}
	socketPath := filepath.Join(finisher.paths.tmuxDir, socket)
	var err error
	if kind == fleetdb.KindNew {
		err = finisher.tmux.KillServer(ctx, socketPath)
	} else {
		err = finisher.tmux.KillPane(ctx, socketPath, pane)
	}
	if err != nil {
		message := exitStderr(err)
		if message == "" {
			message = err.Error()
		}
		gone := tmuxServerGone(message) || kind == fleetdb.KindPane && strings.HasPrefix(message, "can't find pane:")
		if !gone {
			return err
		}
	}
	if kind == fleetdb.KindNew {
		if err := os.Remove(socketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove reaped socket %s: %w", socketPath, err)
		}
	}
	return nil
}

// children resolves one kind of teammate for a chat: the shared table's rows,
// or the flat file when the table has nothing to say.
//
// "Nothing to say" covers both an unreachable table and a table with no row for
// this chat. The second case is the one that matters in practice: a chat that
// registered its teammates through the old flat-file path and exits after the
// cutover has a file and no rows, and reading only the table would leave its
// teammates running forever. A chat registered through the table has rows and
// no file, so the file is never consulted for it.
func (finisher *Finisher) children(
	ctx context.Context,
	kind, id, flatPath string,
) ([]string, error) {
	values, _, err := finisher.database.Shared().Children(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	if len(values) > 0 {
		return values, nil
	}
	return readChildFile(flatPath)
}

// readChildFile reads one flat teammate file from an older install.
func readChildFile(path string) (values []string, returnErr error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close child file %s: %w", path, err))
		}
	}()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		values = append(values, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func waitContext(ctx context.Context, duration time.Duration) error {
	return clock.Real.Sleep(ctx, duration)
}

// pollPaneGone asks tmux up to attempts times, waiting every between checks,
// whether paneID is still on socketPath. probeErr carries the last "could not
// ask" failure — never folded into landed, because a transient tmux error
// must not be read as a pane that already closed (the same distinction
// TmuxClient.PaneExists documents) — joined with any error alive returned.
// alive, when set, runs on each poll that saw the pane still there. Both
// Finisher.Run's graceful-close window and Manager.ConfirmExit's bounded
// caller-side check share this one loop so "is it actually gone yet" has
// exactly one implementation.
func pollPaneGone(
	ctx context.Context,
	tmux TmuxClient,
	socketPath, paneID string,
	attempts int,
	every time.Duration,
	alive func(context.Context) error,
) (landed bool, probeErr, waitErr error) {
	var aliveErr error
	for attempt := 0; attempt < attempts; attempt++ {
		exists, err := tmux.PaneExists(ctx, socketPath, paneID)
		switch {
		case err != nil:
			probeErr = err
		case !exists:
			return true, nil, nil
		case alive != nil:
			if err := alive(ctx); err != nil {
				aliveErr = err
			}
		}
		if attempt < attempts-1 {
			if err := waitContext(ctx, every); err != nil {
				return false, probeErr, err
			}
		}
	}
	return false, errors.Join(probeErr, aliveErr), nil
}
