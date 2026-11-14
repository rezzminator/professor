package kill

import (
	"context"
	"fmt"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// A chat is watched through a terminal, and that terminal is owned by a shell.
// closeViewports already takes down the FIRST viewport shape — a bunker pane
// on the vsct server. This file implements the second shape its doc comment
// names but never handled: a client whose parent merely SPAWNED it rather than
// exec'ing into it, which is what a VS Code terminal tab running
// `zsh -> tmux attach` looks like. That shell survives the chat's death and
// sits at a prompt, and that prompt IS the terminal tab that stays open.
//
// Killing it is safe from inside a SessionEnd hook: the shell lives in the
// terminal's process tree, not the tmux pane's, so signalling it cannot reach
// the exiting engine or the sibling SessionEnd hooks still flushing beside it.

// terminalShells is the set of parent commands that own a terminal. A parent
// outside this set is some other supervisor — a login manager, an editor host,
// an ssh session multiplexer — and closing it would take far more than the tab
// the human asked to close, so it is left alone.
var terminalShells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "dash": true, "ksh": true, "fish": true,
}

// ViewportDeps are the two seams CloseTerminals reads the world through.
type ViewportDeps struct {
	Tmux ViewportTmux
	// Door reads the process table (Door.Procs) and delivers the hang-up. Its
	// Self is the pid the closer must never take down: the hook's own process.
	Door gather.Door
}

// CloseTerminals shuts the terminals this chat is watched through, returning
// the pids it closed and, separately, the reasons it skipped the rest. A
// skipped viewport is NOT a failure — a bunker pane exec'd into its client has
// no shell to close and is meant to be skipped — but it is always named, so a
// terminal that stayed open reports WHY instead of looking like nothing
// happened.
func CloseTerminals(
	ctx context.Context,
	socketPath string,
	dependencies ViewportDeps,
) (closed []int, skipped []string, err error) {
	door := dependencies.Door
	if dependencies.Tmux == nil || door.Procs == nil || door.Kernel == nil {
		return nil, nil, fmt.Errorf("viewport closer needs tmux, a process table, and a signal kernel")
	}
	clients, skipped, err := dependencies.Tmux.Clients(ctx, socketPath)
	if err != nil {
		return nil, nil, err
	}
	if len(clients) == 0 && len(skipped) == 0 {
		return nil, []string{"no client attached — chat was not being watched"}, nil
	}
	var watched []watchedClient
	for _, client := range clients {
		shell, start, reason := clientShell(door, client.PID)
		if reason != "" {
			skipped = append(skipped, reason)
			continue
		}
		watched = append(watched, watchedClient{Client: client, start: start, shell: shell})
	}
	// One order on every OS: each client is detached and waited out, so it
	// restores the screen it drew on, and only then is its shell hung up. A
	// shell hung up first takes the client's tty with it: on macOS the session
	// leader's exit revokes the tty before the client restores the screen, and
	// the terminal keeps the chat's last frame.
	skipped = append(skipped, detachClients(ctx, socketPath, dependencies.Tmux, door, watched)...)
	seen := make(map[int]bool, len(watched))
	for _, client := range watched {
		shell := client.shell
		if seen[shell.PID] {
			continue
		}
		seen[shell.PID] = true
		// SIGHUP is the hangup a terminal owner already knows how to die from:
		// the shell runs its exit traps and the emulator closes the tab.
		outcome, signalErr := door.Signal(gather.Target{Identity: shell, Check: isTerminalShell}, syscall.SIGHUP)
		switch {
		case signalErr != nil:
			skipped = append(skipped, fmt.Sprintf("client %d: hang up shell %d: %v", client.PID, shell.PID, signalErr))
		case outcome == gather.OutcomeGone:
			skipped = append(skipped, fmt.Sprintf(
				"client %d: shell %d ended before the hang-up", client.PID, shell.PID))
		case outcome != gather.OutcomeSent:
			skipped = append(skipped, fmt.Sprintf(
				"client %d: shell %d changed before the hang-up; left alone", client.PID, shell.PID))
		default:
			closed = append(closed, shell.PID)
		}
	}
	return closed, skipped, nil
}

// watchedClient is a client whose terminal shell the closer hangs up: start
// is the client's own start time, so its exit is told from its pid's reuse.
type watchedClient struct {
	Client
	start uint64
	shell gather.Identity
}

// clientDetachBound is how long the detached clients get to restore their
// screens and exit; clientDetachPoll is how often they are read meanwhile.
const (
	clientDetachBound = 2 * time.Second
	clientDetachPoll  = 20 * time.Millisecond
)

// detachClients detaches every watched client, then waits, bounded, until
// each has exited. A client tmux would not detach, or one still running at the
// bound, is named; its shell is still hung up, which closes the terminal with
// the screen as the client left it.
func detachClients(
	ctx context.Context,
	socketPath string,
	tmux ViewportTmux,
	door gather.Door,
	watched []watchedClient,
) (unrestored []string) {
	const after = "; its shell was hung up without it restored"
	var waiting []watchedClient
	for _, client := range watched {
		if err := tmux.DetachClient(ctx, socketPath, client.Name); err != nil {
			unrestored = append(unrestored, fmt.Sprintf("client %d's screen: %v%s", client.PID, err, after))
			continue
		}
		waiting = append(waiting, client)
	}
	deadline := door.Clock.Now().Add(clientDetachBound)
	for len(waiting) > 0 {
		running := waiting[:0]
		for _, client := range waiting {
			alive, err := door.Alive(gather.Identity{PID: client.PID, Start: client.start})
			switch {
			case err != nil:
				unrestored = append(unrestored, fmt.Sprintf("client %d's screen: %v%s", client.PID, err, after))
			case alive:
				running = append(running, client)
			}
		}
		waiting = running
		if len(waiting) == 0 || !door.Clock.Now().Before(deadline) {
			break
		}
		if err := door.Clock.Sleep(ctx, clientDetachPoll); err != nil {
			for _, client := range waiting {
				unrestored = append(unrestored, fmt.Sprintf(
					"client %d's screen: wait for its exit: %v%s", client.PID, err, after))
			}
			return unrestored
		}
	}
	for _, client := range waiting {
		unrestored = append(unrestored, fmt.Sprintf(
			"client %d's screen: still running %s after its detach%s", client.PID, clientDetachBound, after))
	}
	return unrestored
}

// clientShell identifies the terminal shell that spawned client, or names why
// there is none to hang up. The client is read on both sides of the shell's
// identification: a parent that died, its pid going to another process in
// between, leaves the client reparented, so a client still naming the same
// parent since the same start proves the identity read is its spawning shell.
func clientShell(door gather.Door, client int) (shell gather.Identity, clientStart uint64, reason string) {
	before, err := door.Procs.Stat(client)
	if err != nil {
		return gather.Identity{}, 0, fmt.Sprintf("client %d: %v", client, err)
	}
	parent := before.ParentPID
	switch {
	case parent <= 1:
		return gather.Identity{}, 0, fmt.Sprintf(
			"client %d: parent is pid %d — no terminal shell to close",
			client,
			parent,
		)
	case parent == door.Self:
		return gather.Identity{}, 0, fmt.Sprintf("client %d: parent %d is this hook itself", client, parent)
	}
	shell, err = gather.IdentifyProcess(door.Procs, parent)
	if err != nil {
		return gather.Identity{}, 0, fmt.Sprintf("client %d parent %d: %v", client, parent, err)
	}
	after, err := door.Procs.Stat(client)
	if err != nil {
		return gather.Identity{}, 0, fmt.Sprintf("client %d: %v", client, err)
	}
	if after.StartTime != before.StartTime || after.ParentPID != parent {
		return gather.Identity{}, 0, fmt.Sprintf("client %d: left parent %d while it was read", client, parent)
	}
	if shell.Executable == "" {
		return gather.Identity{}, 0, fmt.Sprintf(
			"client %d: parent %d's executable is unreadable, so it is not known to be a terminal shell",
			client,
			parent,
		)
	}
	if holds, _ := isTerminalShell(shell); !holds {
		return gather.Identity{}, 0, fmt.Sprintf(
			"client %d: parent %d is %q, not a terminal shell", client, parent, filepath.Base(shell.Executable))
	}
	return shell, before.StartTime, ""
}

// isTerminalShell holds while the process runs a terminal shell's executable,
// read from the kernel, never from its renameable argv[0].
func isTerminalShell(live gather.Identity) (bool, error) {
	return live.Executable != "" && terminalShells[filepath.Base(live.Executable)], nil
}
