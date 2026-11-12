package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// LockPath is the persistent pane mutex; its flock spans the whole reboot.
func LockPath(sidDir, socketName, pane string) string {
	return filepath.Join(sidDir, "."+socketName+"."+pane+".reloadlock")
}

// ExitMarkPath is the file a reload writes right before it types its own
// /exit and removes once the pane is respawned. The pane mutex alone spans the
// reload's wait for the caller's turn too, when a /exit is still the owner's.
func ExitMarkPath(sidDir, socketName, pane string) string {
	return LockPath(sidDir, socketName, pane) + ".exit"
}

// InFlight reports whether a reload currently holds the pane mutex, so a
// caller leaves a pane alone while a reload works on it. A missing lock file is
// a plain "no"; a lock that cannot be probed is an error, never a "no".
func InFlight(sidDir, socketName, pane string) (inFlight bool, returnErr error) {
	lock, err := os.OpenFile(LockPath(sidDir, socketName, pane), os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open reload lock: %w", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("close reload lock %s: %w", LockPath(sidDir, socketName, pane), err),
			)
		}
	}()
	// A shared probe conflicts with Run's exclusive lock, never with another probe.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, fmt.Errorf("probe reload lock: %w", err)
	}
	return false, nil
}

// ExitTyped reports whether a reload holds the pane mutex and has typed its own
// /exit (its exit mark is written), so a SessionEnd hook tells a reload's /exit
// (the pane is being rebooted — leave its terminal alone) from its owner's, also
// one typed while the reload still waits for the chat's turn to end. A lock or a
// mark that cannot be read is an error, never a "no".
func ExitTyped(sidDir, socketName, pane string) (bool, error) {
	inFlight, err := InFlight(sidDir, socketName, pane)
	if err != nil || !inFlight {
		return false, err
	}
	if _, err := os.Stat(ExitMarkPath(sidDir, socketName, pane)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("read reload exit mark: %w", err)
	}
	return true, nil
}

// markExit writes the exit mark; the returned unmark removes it and may run
// more than once.
func markExit(path string) (unmark func(io.Writer), err error) {
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return nil, fmt.Errorf("write reload exit mark: %w", err)
	}
	return func(stderr io.Writer) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "pfm chat reload: remove exit mark %s: %v\n", path, err)
		}
	}, nil
}

// yieldToOwner reads whether the chat ended before the reload typed its own
// /exit — its owner typed one, or its engine died. A gone server or a pane
// that left it is ended; a dead pane the reload's remain-on-exit kept is
// closed, so the owner's exit ends the chat as it would have without a
// reload. A pane read that fails is an error, never an ending.
func yieldToOwner(ctx context.Context, tmux Tmux, request Request) (ended bool, err error) {
	panes, err := tmux.ListPanes(ctx, request.SocketPath)
	if errors.Is(err, gather.ErrServerGone) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("check whether the chat ended before the reload's /exit: %w", err)
	}
	for _, pane := range panes {
		if pane.ID != request.Pane {
			continue
		}
		if !pane.Dead {
			return false, nil
		}
		if err := tmux.KillPane(ctx, request.SocketPath, request.Pane); err != nil {
			return true, fmt.Errorf("close the ended chat's pane %s: %w", request.Pane, err)
		}
		return true, nil
	}
	return true, nil
}

// Receipt is the worker's one line on what the reload did to the pane.
func (result Result) Receipt() string {
	if result.Ended {
		return "the chat ended before its reboot, nothing respawned"
	}
	return "respawned in place"
}
