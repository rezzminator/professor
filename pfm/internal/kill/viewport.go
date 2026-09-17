package kill

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"hostops/pfm/internal/deps"
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

// ProcessInfo is the slice of the process table the viewport closer reads.
type ProcessInfo struct {
	PID  int
	PPID int
	Comm string
}

// ProcessTable reads one process's parentage. An interface so a test can state
// a tree instead of spawning one.
type ProcessTable interface {
	Info(ctx context.Context, pid int) (ProcessInfo, error)
}

// Signaller delivers the close. Separated from the lookup so a test can assert
// exactly which pids WOULD be signalled without signalling anything.
type Signaller interface {
	Signal(pid int, signal syscall.Signal) error
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

// CommandProcessTable reads the real process table through ps.
type CommandProcessTable struct{ Binary string }

// Info returns one process's pid, parent, and command name.
func (table CommandProcessTable) Info(
	ctx context.Context,
	pid int,
) (ProcessInfo, error) {
	binary := table.Binary
	if binary == "" {
		binary = deps.Executable("ps")
	}
	output, err := exec.CommandContext(
		ctx, binary, "-o", "pid=,ppid=,comm=", "-p", strconv.Itoa(pid),
	).Output()
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("read process %d: %w", pid, err)
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 3 {
		return ProcessInfo{}, fmt.Errorf(
			"process %d returned %d fields, want pid ppid comm", pid, len(fields),
		)
	}
	self, err := strconv.Atoi(fields[0])
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse pid %q: %w", fields[0], err)
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("parse ppid %q: %w", fields[1], err)
	}
	return ProcessInfo{PID: self, PPID: parent, Comm: fields[2]}, nil
}

// ProcessSignaller delivers a real signal.
type ProcessSignaller struct{}

// Signal sends one signal to one pid.
func (ProcessSignaller) Signal(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}

// ViewportDeps are the three seams CloseTerminals reads the world through.
type ViewportDeps struct {
	Tmux      ClientLister
	Processes ProcessTable
	Signals   Signaller
	// Self is the pid the closer must never take down — the hook's own
	// process. Zero means os.Getpid().
	Self int
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
	if dependencies.Tmux == nil || dependencies.Processes == nil ||
		dependencies.Signals == nil {
		return nil, nil, fmt.Errorf("viewport closer needs tmux, process table, and signaller")
	}
	self := dependencies.Self
	if self == 0 {
		self = os.Getpid()
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
		info, infoErr := dependencies.Processes.Info(ctx, client)
		if infoErr != nil {
			skipped = append(skipped, fmt.Sprintf("client %d: %v", client, infoErr))
			continue
		}
		parent, parentErr := dependencies.Processes.Info(ctx, info.PPID)
		if parentErr != nil {
			skipped = append(skipped, fmt.Sprintf("client %d parent %d: %v", client, info.PPID, parentErr))
			continue
		}
		name := filepath.Base(parent.Comm)
		switch {
		case parent.PID <= 1:
			skipped = append(skipped, fmt.Sprintf(
				"client %d: parent is pid %d — no terminal shell to close", client, parent.PID))
			continue
		case parent.PID == self:
			skipped = append(skipped, fmt.Sprintf(
				"client %d: parent %d is this hook itself", client, parent.PID))
			continue
		case !terminalShells[name]:
			skipped = append(skipped, fmt.Sprintf(
				"client %d: parent %d is %q, not a terminal shell", client, parent.PID, name))
			continue
		case seen[parent.PID]:
			continue
		}
		seen[parent.PID] = true
		// SIGHUP is the hangup a terminal owner already knows how to die from:
		// the shell runs its exit traps and the emulator closes the tab.
		if signalErr := dependencies.Signals.Signal(parent.PID, syscall.SIGHUP); signalErr != nil {
			skipped = append(skipped, fmt.Sprintf(
				"client %d: hang up shell %d: %v", client, parent.PID, signalErr))
			continue
		}
		closed = append(closed, parent.PID)
	}
	return closed, skipped, nil
}
