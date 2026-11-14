package action

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// launchPoll, launchQuiet and launchDeadline pace the launch watch: how often
// it reads the pane it just created, how long a screen with no engine running
// on it (a launcher still starting one) must hold still to count as up, and
// how long it watches one that never settles before it reports the open as
// done anyway.
var (
	launchPoll     = 250 * time.Millisecond
	launchQuiet    = 5 * time.Second
	launchDeadline = 15 * time.Second
)

// launchWordsLines is how many of a dead pane's last non-empty lines its
// failure quotes: enough for a refusal and its reason, never a whole screen.
const launchWordsLines = 6

// watchLaunch reads the pane a chat server was just created around until the
// chat proves it is up, so an open never reports a chat that is not there.
//
// While it watches, the pane keeps its process's last screen when that process
// exits (remain-on-exit), so a chat that dies at launch is a failed open
// carrying the pane's own words, its dead server killed rather than left for a
// lister to count; a server already gone is a failed open too. A pane met by
// Claude Code's folder-trust dialog stays up untouched — the dialog is the
// human's decision, and the key that clears it is never pressed here — and
// comes back as a warning carrying the attach command. A pane showing the same
// non-empty screen twice is up when an engine on it (paneEngines) ran both at
// the poll before the screen last changed and at that change — words a launcher
// or a failed first run printed as an engine just started are not its own — or
// when the screen held still for launchQuiet; one never settling is opened once
// launchDeadline passes. Every pane left alive gets remain-on-exit back off.
func (executor *Executor) watchLaunch(
	ctx context.Context,
	server ChatServer,
	name string,
	engineCommands ...string,
) ([]string, error) {
	socket := server.Socket
	if name == "" {
		name = socket
	}
	if err := executor.tmux.SetRemainOnExit(ctx, socket, true); err != nil {
		// Without it a pane that dies now takes its words with it; the watch
		// still tells a dead launch from a live one.
		obs.Logger(ctx).WarnContext(
			ctx, "launch watch: set remain-on-exit failed",
			"err", err, "socket", socket,
		)
	}
	engines := enginePaneCommands(engineCommands...)
	deadline := clock.Real.Now().Add(launchDeadline)
	previous, stillSince := "", clock.Real.Now()
	var running, painters []int // the engines at the last poll; those also there before the screen last changed
	for {
		pane, err := executor.tmux.LaunchPane(ctx, socket)
		if err != nil && ctx.Err() != nil {
			// The read failed because the caller stopped waiting, not
			// because the server is gone.
			return executor.keepLaunch(ctx, socket, []string{unwatched(name, socket, ctx.Err())}), nil
		}
		if err != nil {
			// Only tmux's word that the server is dead reports the launch
			// dead: a running, unresponsive or unprobed server leaves dead or
			// up unknown, and a live server is never reported dead.
			state, probeErr := executor.tmux.ServerState(context.WithoutCancel(ctx), socket)
			if state != gather.ServerDead {
				warning := fmt.Sprintf(
					"pfm could not read %s's launch pane (%v); its server is %s: whether it is up is unknown — tmux -L %s attach -t %s",
					name,
					errors.Join(err, probeErr),
					state,
					socket,
					socket,
				)
				return executor.keepLaunch(ctx, socket, []string{warning}), nil
			}
		}
		if err != nil {
			// The last screen an earlier poll read is all that is left of
			// what the chat printed; none read is the loss, said as such.
			printed := "what it printed was lost"
			if previous != "" {
				printed = "its pane last read: " + paneWords(previous)
			}
			return nil, retiredLaunch(ctx, socket, fmt.Errorf(
				"the chat %s died at launch: its server on socket %s is gone (%w) — %s",
				name, socket, err, printed,
			))
		}
		screen := pane.Screen
		if pane.Dead {
			return nil, executor.deadLaunch(ctx, name, socket, screen)
		}
		if pfmengine.ClaudeTrustDialog(screen) {
			refusal := spawn.Result{Socket: socket, Session: socket}.TrustRefusal(name, server.CWD)
			return executor.keepLaunch(ctx, socket, []string{refusal}), nil
		}
		now, onPane := clock.Real.Now(), executor.paneEngines(ctx, pane, engines)
		if screen != previous {
			previous, stillSince = screen, now
			painters = slices.DeleteFunc(slices.Clone(onPane),
				func(engine int) bool { return !slices.Contains(running, engine) })
		} else if strings.TrimSpace(screen) != "" && (now.Sub(stillSince) >= launchQuiet ||
			slices.ContainsFunc(onPane, func(engine int) bool { return slices.Contains(painters, engine) })) {
			return executor.keepLaunch(ctx, socket, nil), nil
		}
		running = onPane
		if !now.Before(deadline) {
			return executor.keepLaunch(ctx, socket, nil), nil
		}
		if err := clock.Real.Sleep(ctx, launchPoll); err != nil {
			return executor.keepLaunch(ctx, socket, []string{unwatched(name, socket, err)}), nil
		}
	}
}

// paneEngines is every engine running on the pane: 0 for its own command, else
// the pid of each engine process on its terminal, where a resume row's shell or
// an agent row's pfm stays in front; an unreadable process table answers none.
func (executor *Executor) paneEngines(ctx context.Context, pane LaunchPaneState, engines map[string]bool) []int {
	if isEngineCommand(pane.Command, engines) {
		return []int{0}
	}
	if pane.TTY == "" {
		return nil
	}
	processes, err := executor.processes.Processes(ctx)
	if err != nil {
		obs.Logger(ctx).WarnContext(ctx, "launch watch: read the process table failed", "err", err, "tty", pane.TTY)
		return nil
	}
	var running []int
	for _, process := range processes {
		if process.TTY == pane.TTY && len(process.Argv) > 0 &&
			isEngineCommand(filepath.Base(process.Argv[0]), engines) {
			running = append(running, process.PID)
		}
	}
	return running
}

// unwatched is the warning of a launch whose caller stopped waiting: the chat
// may be up or not, and saying nothing would read as up.
func unwatched(name, socket string, cause error) string {
	return fmt.Sprintf(
		"pfm stopped watching %s's launch (%v): whether it is up is unknown — tmux -L %s attach -t %s",
		name, cause, socket, socket,
	)
}

// deadLaunch fails a launch whose pane's process exited: its last words, its server killed, its seat retired.
func (executor *Executor) deadLaunch(ctx context.Context, name, socket, screen string) error {
	words := paneWords(screen)
	if err := executor.tmux.KillServer(context.WithoutCancel(ctx), socket); err != nil {
		obs.Logger(ctx).WarnContext(
			ctx, "launch watch: kill the dead launch's server failed",
			"err", err, "socket", socket,
		)
		return fmt.Errorf(
			"the chat %s exited at launch — its pane said: %s (its dead server stays: tmux -L %s kill-server failed: %w)",
			name,
			words,
			socket,
			err,
		)
	}
	return retiredLaunch(ctx, socket, fmt.Errorf("the chat %s exited at launch — its pane said: %s", name, words))
}

// retireLaunchSeat retires a dead launch's seat, which no SessionEnd retires; tests replace it.
var retireLaunchSeat = spawn.RetireTmuxSeat

// retiredLaunch retires a dead launch's seat and returns died, naming a seat the retire left.
func retiredLaunch(ctx context.Context, socket string, died error) error {
	if err := retireLaunchSeat(context.WithoutCancel(ctx), socket); err != nil {
		return fmt.Errorf("%w (its seat stays: %w)", died, err)
	}
	return died
}

// keepLaunch turns remain-on-exit back off on a pane the watch leaves alive; a
// failure to is one more warning, since the chat's pane would then outlive it.
func (executor *Executor) keepLaunch(ctx context.Context, socket string, warnings []string) []string {
	if err := executor.tmux.SetRemainOnExit(context.WithoutCancel(ctx), socket, false); err != nil {
		obs.Logger(ctx).WarnContext(
			ctx, "launch watch: clear remain-on-exit failed",
			"err", err, "socket", socket,
		)
		warnings = append(warnings, fmt.Sprintf(
			"remain-on-exit stayed on for %s (%v): when the chat ends its dead pane stays — tmux -L %s kill-server clears it",
			socket,
			err,
			socket,
		))
	}
	return warnings
}

// paneWords is a dead pane's last non-empty lines on one line.
func paneWords(screen string) string {
	var lines []string
	for _, line := range strings.Split(screen, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		return "nothing"
	}
	if len(lines) > launchWordsLines {
		lines = lines[len(lines)-launchWordsLines:]
	}
	return strings.Join(lines, " | ")
}

// launchWarnings is a plan's warnings followed by its launch watch's, in a new
// slice so the plan's own stays as Synthesize built it.
func launchWarnings(planned, held []string) []string {
	if len(held) == 0 {
		return planned
	}
	return append(append([]string(nil), planned...), held...)
}
