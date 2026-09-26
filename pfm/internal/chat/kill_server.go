package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// KillServer ends one chat's tmux server and removes its socket, crumbs, and
// branch-seat marker.
func KillServer(ctx context.Context, resolved paths.Values, socket string) (err error) {
	end := obs.Transition(ctx, "chat", "live", "ended", "KillServer")
	defer func() { end(err) }()
	tmux := action.TmuxExecutor{TmuxDir: resolved.TmuxDir}
	if err := tmux.KillServer(ctx, socket); err != nil {
		if tmux.SocketAlive(ctx, socket) {
			return err
		}
	}
	_ = os.Remove(filepath.Join(resolved.TmuxDir, socket))
	entries, _ := os.ReadDir(resolved.SIDDir)
	for _, entry := range entries {
		if name, _, ok := gather.ParseCrumbName(entry.Name()); ok && name == socket {
			_ = os.Remove(filepath.Join(resolved.SIDDir, entry.Name()))
		}
	}
	state := fleetdb.OpenSharedState(ctx, resolved)
	clearErr := state.ClearBranchSeat(ctx, socket)
	closeErr := state.Close()
	if clearErr != nil || closeErr != nil {
		return fmt.Errorf("clear branch marker after ending %s: %w", socket, errors.Join(clearErr, closeErr))
	}
	return nil
}
