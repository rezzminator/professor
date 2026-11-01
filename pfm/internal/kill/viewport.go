package kill

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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

// ClientLister names the one tmux read the closer needs.
type ClientLister interface {
	ClientPIDs(ctx context.Context, socketPath string) ([]int, error)
}

// ClientPIDs lists the `tmux attach` processes currently attached to this
// server — one per terminal the chat is being watched through.
func (tmux TmuxKiller) ClientPIDs(
	ctx context.Context,
	socketPath string,
) ([]int, error) {
	output, err := tmux.command(
		ctx, socketPath, "list-clients", "-F", "#{client_pid}",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("list clients on %s: %w", socketPath, err)
	}
	var pids []int
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, convErr := strconv.Atoi(line)
		if convErr != nil {
			return nil, fmt.Errorf("parse client pid %q: %w", line, convErr)
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// ViewportDeps are the two seams CloseTerminals reads the world through.
type ViewportDeps struct {
	Tmux ClientLister
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
	clients, err := dependencies.Tmux.ClientPIDs(ctx, socketPath)
	if err != nil {
		return nil, nil, err
	}
	if len(clients) == 0 {
		return nil, []string{"no client attached — chat was not being watched"}, nil
	}
	seen := make(map[int]bool, len(clients))
	for _, client := range clients {
		shell, reason := clientShell(door, client)
		switch {
		case reason != "":
			skipped = append(skipped, reason)
			continue
		case seen[shell.PID]:
			continue
		}
		seen[shell.PID] = true
		// SIGHUP is the hangup a terminal owner already knows how to die from:
		// the shell runs its exit traps and the emulator closes the tab.
		outcome, signalErr := door.Signal(gather.Target{Identity: shell, Check: isTerminalShell}, syscall.SIGHUP)
		switch {
		case signalErr != nil:
			skipped = append(skipped, fmt.Sprintf("client %d: hang up shell %d: %v", client, shell.PID, signalErr))
		case outcome == gather.OutcomeGone:
			skipped = append(skipped, fmt.Sprintf("client %d: shell %d ended before the hang-up", client, shell.PID))
		case outcome != gather.OutcomeSent:
			skipped = append(skipped, fmt.Sprintf(
				"client %d: shell %d changed before the hang-up; left alone", client, shell.PID))
		default:
			closed = append(closed, shell.PID)
		}
	}
	return closed, skipped, nil
}

// clientShell identifies the terminal shell that spawned client, or names why
// there is none to hang up. The client is read on both sides of the shell's
// identification: a parent that died, its pid going to another process in
// between, leaves the client reparented, so a client still naming the same
// parent since the same start proves the identity read is its spawning shell.
func clientShell(door gather.Door, client int) (gather.Identity, string) {
	before, err := door.Procs.Stat(client)
	if err != nil {
		return gather.Identity{}, fmt.Sprintf("client %d: %v", client, err)
	}
	parent := before.ParentPID
	switch {
	case parent <= 1:
		return gather.Identity{}, fmt.Sprintf(
			"client %d: parent is pid %d — no terminal shell to close",
			client,
			parent,
		)
	case parent == door.Self:
		return gather.Identity{}, fmt.Sprintf("client %d: parent %d is this hook itself", client, parent)
	}
	shell, err := gather.IdentifyProcess(door.Procs, parent)
	if err != nil {
		return gather.Identity{}, fmt.Sprintf("client %d parent %d: %v", client, parent, err)
	}
	after, err := door.Procs.Stat(client)
	if err != nil {
		return gather.Identity{}, fmt.Sprintf("client %d: %v", client, err)
	}
	if after.StartTime != before.StartTime || after.ParentPID != parent {
		return gather.Identity{}, fmt.Sprintf("client %d: left parent %d while it was read", client, parent)
	}
	if shell.Executable == "" {
		return gather.Identity{}, fmt.Sprintf(
			"client %d: parent %d's executable is unreadable, so it is not known to be a terminal shell",
			client,
			parent,
		)
	}
	if holds, _ := isTerminalShell(shell); !holds {
		return gather.Identity{}, fmt.Sprintf(
			"client %d: parent %d is %q, not a terminal shell", client, parent, filepath.Base(shell.Executable))
	}
	return shell, ""
}

// isTerminalShell holds while the process runs a terminal shell's executable,
// read from the kernel, never from its renameable argv[0].
func isTerminalShell(live gather.Identity) (bool, error) {
	return live.Executable != "" && terminalShells[filepath.Base(live.Executable)], nil
}
