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

	"hostops/pfm/internal/clock"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

const (
	defaultExitDelay    = 1500 * time.Millisecond
	defaultPollEvery    = time.Second
	defaultPollAttempts = 20
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
			claudeRoots = pfmconfig.Defaults(
				resolved.Home,
				resolved.Roots[pfmengine.Claude],
			).ProjectRoots()
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
	return &Finisher{
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

// Run performs the delayed graceful close, fallback kill, cleanup, refresh,
// post-exit kill, and teammate reap.
func (finisher *Finisher) Run(
	ctx context.Context,
	args ExitArgs,
) error {
	if args.Engine != pfmengine.Claude && args.Engine != pfmengine.Codex {
		return fmt.Errorf("unknown kill-exit engine %q", args.Engine)
	}
	if args.ID == "" || args.SocketPath == "" || args.PaneID == "" {
		return errors.New("kill-exit requires id, socket, and pane")
	}
	if err := waitContext(ctx, finisher.delay); err != nil {
		return err
	}

	// Read the viewports BEFORE the chat dies: its clients vanish with its server,
	// and they are the only evidence of which panes were watching it.
	viewports := finisher.viewportPanes(ctx, args.SocketPath)

	command := "/exit"
	if args.Engine == pfmengine.Codex {
		command = "/quit"
	}
	_ = finisher.tmux.SendLine(ctx, args.SocketPath, args.PaneID, command)
	for attempt := 0; attempt < finisher.pollAttempts; attempt++ {
		if !finisher.tmux.PaneExists(ctx, args.SocketPath, args.PaneID) {
			break
		}
		if err := waitContext(ctx, finisher.pollEvery); err != nil {
			return err
		}
	}
	_ = finisher.tmux.KillPane(ctx, args.SocketPath, args.PaneID)
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
	} else if err := finisher.recordPostExitKill(ctx, args); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	if err := finisher.reapTeammates(ctx, args.ID); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return errors.Join(cleanupErrors...)
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

// recordPostExitKill re-asserts the kill after the post-exit index refresh so
// the chat stays killed whatever the exit flush wrote, keeping the original
// kill time.
func (finisher *Finisher) recordPostExitKill(
	ctx context.Context,
	args ExitArgs,
) error {
	killedAt := finisher.now().Unix()
	if killed, exists, err := finisher.database.Killed(ctx, args.ID); err != nil {
		return err
	} else if exists {
		killedAt = killed.KilledAt
	}
	if err := finisher.database.Kill(ctx, store.Killed{
		ID:       args.ID,
		Engine:   args.Engine,
		KilledAt: killedAt,
	}); err != nil {
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
	state := finisher.database.Shared()

	detachedPath := filepath.Join(
		finisher.paths.home,
		".claude",
		".cc-new-children",
		id,
	)
	detached, err := finisher.children(ctx, fleetdb.KindNew, id, detachedPath)
	if err != nil {
		reapErrors = append(reapErrors, err)
	}
	for _, socket := range detached {
		socket = strings.TrimSpace(socket)
		if socket == "" {
			continue
		}
		socketPath := filepath.Join(finisher.paths.tmuxDir, socket)
		_ = finisher.tmux.KillServer(ctx, socketPath)
		if err := os.Remove(socketPath); err != nil &&
			!errors.Is(err, fs.ErrNotExist) {
			reapErrors = append(reapErrors, err)
		}
	}
	if err := state.ClearChildren(ctx, fleetdb.KindNew, id); err != nil {
		reapErrors = append(reapErrors, err)
	}
	if err := os.Remove(detachedPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		reapErrors = append(reapErrors, err)
	}

	panePath := filepath.Join(
		finisher.paths.home,
		".claude",
		".cc-pane-children",
		id,
	)
	panes, err := finisher.children(ctx, fleetdb.KindPane, id, panePath)
	if err != nil {
		reapErrors = append(reapErrors, err)
	}
	for _, value := range panes {
		socket, pane, found := strings.Cut(value, "\t")
		if !found || pane == "" {
			continue
		}
		_ = finisher.tmux.KillPane(
			ctx,
			filepath.Join(finisher.paths.tmuxDir, socket),
			pane,
		)
	}
	if err := state.ClearChildren(ctx, fleetdb.KindPane, id); err != nil {
		reapErrors = append(reapErrors, err)
	}
	if err := os.Remove(panePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		reapErrors = append(reapErrors, err)
	}
	return errors.Join(reapErrors...)
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
