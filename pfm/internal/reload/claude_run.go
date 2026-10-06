package reload

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// claudeRun is the respawn line for a Claude seat. It owns nothing: the strip,
// the autonomy posture and the system prompt all come from the one spawn door,
// so a chat that reboots in place comes back with exactly what a fresh launch
// would have carried.
func claudeRun(request Request) (string, error) {
	effort, err := action.ClaudeEffort(request.Effort)
	if err != nil {
		return "", fmt.Errorf("resolve claude respawn effort: %w", err)
	}
	spawn := action.ClaudeSpawn{
		Purpose:    action.PurposeResume,
		Account:    request.Account,
		Cache1H:    &request.Cache1H,
		Home:       request.Home,
		Machine:    request.Machine,
		Model:      request.Model,
		Effort:     effort,
		PromptFile: request.PromptChannel,
		Name:       request.Name,
	}
	if request.fresh {
		spawn.Purpose, spawn.SessionID = action.PurposeInteractive, request.SessionID
	} else {
		spawn.Resume = request.SessionID
	}
	run, err := spawn.ShellCommand()
	if err != nil {
		return "", fmt.Errorf("render claude respawn command: %w", err)
	}
	return run, nil
}

// respawnClaimedPane protects the interval before the replacement writes any
// engine session marker, using the same account ownership as new launches.
func respawnClaimedPane(ctx context.Context, tmux Tmux, request Request, run string) (returnErr error) {
	account, err := claudelaunch.ConfigDirFromRun(run)
	if err != nil {
		return err
	}
	guard, err := gather.AcquireAccountGuard(account, true)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, guard.Close()) }()
	// A failed tmux client may still have created the replacement. Clear its
	// pending claim only after a fresh replacement proves the engine terminated.
	cleanup := func(cause error) error {
		if guard == nil {
			return cause
		}
		if err := tmux.Respawn(
			context.Background(),
			request.SocketPath,
			request.Pane,
			request.CWD,
			"exit 1",
		); err != nil {
			return errors.Join(cause, fmt.Errorf("terminate unrecorded reload pane: %w", err))
		}
		return errors.Join(cause, guard.Abort())
	}
	if err := tmux.Respawn(ctx, request.SocketPath, request.Pane, request.CWD, run); err != nil {
		return cleanup(err)
	}
	if guard == nil {
		return nil
	}
	panes, err := tmux.ListPanes(ctx, request.SocketPath)
	if err != nil {
		return cleanup(fmt.Errorf("read reloaded account pane pid: %w", err))
	}
	for _, pane := range panes {
		if pane.ID != request.Pane {
			continue
		}
		if pane.Dead {
			return guard.Abort()
		}
		if err := guard.Record(pane.PID); err != nil {
			return cleanup(err)
		}
		return nil
	}
	return cleanup(fmt.Errorf("reloaded account pane %s is missing", request.Pane))
}
