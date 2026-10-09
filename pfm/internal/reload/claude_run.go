package reload

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
)

// claudeRun is the respawn line for a Claude seat. It owns nothing: the strip,
// the autonomy posture and the system prompt all come from the one spawn door,
// so a chat that reboots in place comes back with exactly what a fresh launch
// would have carried.
func claudeRun(request Request) (string, error) {
	spawn, err := claudeSpawn(request)
	if err != nil {
		return "", err
	}
	run, err := spawn.ShellCommand()
	if err != nil {
		return "", fmt.Errorf("render claude respawn command: %w", err)
	}
	return run, nil
}

// claudeSpawn is the one spawn the respawn line and its prompt verdict share.
func claudeSpawn(request Request) (action.ClaudeSpawn, error) {
	effort, err := action.ClaudeEffort(request.Effort)
	if err != nil {
		return action.ClaudeSpawn{}, fmt.Errorf("resolve claude respawn effort: %w", err)
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
	return spawn, nil
}

// respawnPromptWarning is why the reloaded Claude chat will start without its system
// prompt file, or "" when it carries what a fresh launch would carry.
func respawnPromptWarning(request Request) (string, error) {
	if request.Engine != pfmengine.Claude {
		return "", nil
	}
	spawn, err := claudeSpawn(request)
	if err != nil {
		return "", err
	}
	return spawn.PromptWarning(), nil
}

// warnPromptMissing tells whoever asked for the reload, before the pane is
// respawned, that the reloaded Claude chat will start without its fleet
// prompt, and returns the warning for the pane display after the respawn.
func warnPromptMissing(request Request, stderr io.Writer) string {
	warning, err := respawnPromptWarning(request)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: check the reloaded chat's system prompt: %v\n", err)
		return ""
	}
	if warning != "" {
		launchnotice.Hold(stderr, "pfm chat reload", []string{warning})
	}
	return warning
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
