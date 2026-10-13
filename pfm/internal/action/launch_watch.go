package action

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// launchPoll, launchQuiet and launchDeadline pace the launch watch: how often
// it reads the pane it just created, how long a screen with no engine running
// on it (a launcher still starting one) must hold still to count as up, and
// how long it watches one that never settles before it
// reports the open as done anyway.
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
// While it watches, the pane keeps its process's last screen when that
// process exits (remain-on-exit), so a chat that dies at launch is a failed
// open carrying the pane's own words, its dead server killed rather than left
// for a lister to count; a server already gone is a failed open too. A pane
// met by Claude Code's folder-trust dialog stays up untouched — the dialog is
// the human's decision, and the key that clears it is never pressed here —
// and comes back as a warning carrying the attach command. A pane showing the
// same non-empty screen twice with an engine running on it (engineOnPane) is
// up, as is one whose screen held still for launchQuiet with none — a
// launcher that prints and then waits on the engine it starts must not read
// as up the moment it prints; one that never settles is reported as opened
// once launchDeadline passes. Every pane left alive gets
// remain-on-exit back off, so a chat that ends later takes its server with it
// as it always did.
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
	for {
		pane, err := executor.tmux.LaunchPane(ctx, socket)
		if err != nil && ctx.Err() != nil {
			// The read failed because the caller stopped waiting, not
			// because the server is gone.
			return executor.keepLaunch(ctx, socket, []string{unwatched(name, socket, ctx.Err())}), nil
		}
		if err != nil && executor.tmux.SocketAlive(context.WithoutCancel(ctx), socket) {
			// The server runs; only its pane would not read. Dead or up is
			// unknown, and a live server is never reported dead.
			warning := fmt.Sprintf(
				"pfm could not read %s's launch pane (%v): whether it is up is unknown — tmux -L %s attach -t %s",
				name, err, socket, socket,
			)
			return executor.keepLaunch(ctx, socket, []string{warning}), nil
		}
		if err != nil {
			return nil, fmt.Errorf(
				"the chat %s died at launch: its server on socket %s is gone (%w) — what it printed was lost",
				name, socket, err,
			)
		}
		screen := pane.Screen
		if pane.Dead {
			return nil, executor.deadLaunch(ctx, name, socket, screen)
		}
		if pfmengine.ClaudeTrustDialog(screen) {
			refusal := spawn.Result{Socket: socket, Session: socket}.TrustRefusal(name, server.CWD)
			return executor.keepLaunch(ctx, socket, []string{refusal}), nil
		}
		now := clock.Real.Now()
		if screen != previous {
			previous, stillSince = screen, now
		} else if strings.TrimSpace(screen) != "" &&
			(now.Sub(stillSince) >= launchQuiet || executor.engineOnPane(ctx, pane, engines)) {
			return executor.keepLaunch(ctx, socket, nil), nil
		}
		if !now.Before(deadline) {
			return executor.keepLaunch(ctx, socket, nil), nil
		}
		if err := clock.Real.Sleep(ctx, launchPoll); err != nil {
			return executor.keepLaunch(ctx, socket, []string{unwatched(name, socket, err)}), nil
		}
	}
}

// engineOnPane reports whether an engine runs on the pane: as its own command,
// or as any process on its terminal — a resume row's compound line keeps its
// shell in front and an agent row runs the engine as pfm's child, so neither
// pane's command names the engine. A process table that cannot be read
// answers no, leaving the pane to the quiet rule.
func (executor *Executor) engineOnPane(ctx context.Context, pane LaunchPaneState, engines map[string]bool) bool {
	if isEngineCommand(pane.Command, engines) {
		return true
	}
	if pane.TTY == "" {
		return false
	}
	processes, err := executor.processes.Processes(ctx)
	if err != nil {
		obs.Logger(ctx).WarnContext(
			ctx, "launch watch: read the process table failed",
			"err", err, "tty", pane.TTY,
		)
		return false
	}
	for _, process := range processes {
		if process.TTY == pane.TTY && len(process.Argv) > 0 &&
			isEngineCommand(filepath.Base(process.Argv[0]), engines) {
			return true
		}
	}
	return false
}

// unwatched is the warning of a launch whose caller stopped waiting: the chat
// may be up or not, and saying nothing would read as up.
func unwatched(name, socket string, cause error) string {
	return fmt.Sprintf(
		"pfm stopped watching %s's launch (%v): whether it is up is unknown — tmux -L %s attach -t %s",
		name, cause, socket, socket,
	)
}

// deadLaunch is the failure of a pane whose process exited at launch: the
// pane's last words, its dead server killed so nothing lists it as a chat.
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
	return fmt.Errorf("the chat %s exited at launch — its pane said: %s", name, words)
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
