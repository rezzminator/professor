package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// ErrEndRefused is KillServer's refusal: the server was left running, because
// the chat's pane could not be proved its sole pane or tmux never said it died.
var ErrEndRefused = errors.New("end refused")

// serverDeathAttempts probes, serverDeathEvery apart, bound the wait for a
// sole-pane kill-server to leave a dead server.
const (
	serverDeathAttempts = 20
	serverDeathEvery    = 25 * time.Millisecond
)

// KillServer ends one chat's tmux server, only while paneID is the server's
// sole pane, then removes its socket, crumbs, branch-seat marker and tmux seat. A caller
// holding no pane id passes "": the server's one pane stands in for it, and a
// server holding several is refused. A picker row or a scan is a view: a pane
// can join the server after it, so kill.TmuxKiller.KillServerIfSolePane decides
// inside tmux, and only tmux's word that the server is dead clears its handles.
func KillServer(ctx context.Context, resolved paths.Values, socket, paneID string) (err error) {
	if filepath.IsAbs(socket) {
		return errors.New("KillServer requires a tmux socket name; use EndResolved for a native Herdr seat")
	}
	end := obs.Transition(ctx, "chat", "live", "ended", "KillServer")
	defer func() { end(err) }()
	socketPath := filepath.Join(resolved.TmuxDir, socket)
	if paneID == "" {
		state, panes, probeErr := gather.ProbeServer(ctx, "", socketPath)
		if state == gather.ServerDead {
			return clearServerHandles(ctx, resolved, socket)
		}
		if state != gather.ServerAlive || len(panes) != 1 {
			return fmt.Errorf(
				"server %s is %s with %d panes: no sole pane proves the server is one chat's own: %w",
				socket,
				state,
				len(panes),
				errors.Join(probeErr, ErrEndRefused),
			)
		}
		paneID = panes[0].PaneID
	}
	soleErr := kill.TmuxKiller{}.KillServerIfSolePane(ctx, socketPath, paneID)
	// kill-server returns before the server has exited: a probe in between
	// can still answer alive. Only a dead answer within the bound ends the
	// chat; an unresponsive server is not waited on twice.
	tmux := action.TmuxExecutor{TmuxDir: resolved.TmuxDir}
	var state gather.ServerState
	var probeErr error
	for attempt := range serverDeathAttempts {
		state, probeErr = tmux.ServerState(ctx, socket)
		if state == gather.ServerDead || state == gather.ServerUnresponsive || attempt == serverDeathAttempts-1 {
			break
		}
		if err := clock.Real.Sleep(ctx, serverDeathEvery); err != nil {
			probeErr = errors.Join(probeErr, err)
			break
		}
	}
	if state != gather.ServerDead {
		return fmt.Errorf(
			"server %s is still %s after kill-server, which tmux runs only while pane %s is the server's sole pane; "+
				"a server holding another pane is never ended: %w",
			socket,
			state,
			paneID,
			errors.Join(soleErr, probeErr, ErrEndRefused),
		)
	}
	return clearServerHandles(ctx, resolved, socket)
}

// clearServerHandles removes a dead server's socket, crumbs, branch-seat
// marker and tmux seat. The engine's SessionEnd hook removes the seat when it
// runs, but a hangup can end the engine before its hooks do: the server tmux
// says is dead holds no live seat either way.
func clearServerHandles(ctx context.Context, resolved paths.Values, socket string) error {
	_ = os.Remove(filepath.Join(resolved.TmuxDir, socket))
	entries, _ := os.ReadDir(resolved.SIDDir)
	for _, entry := range entries {
		if name, _, ok := gather.ParseCrumbName(entry.Name()); ok && name == socket {
			_ = os.Remove(filepath.Join(resolved.SIDDir, entry.Name()))
		}
	}
	shared := fleetdb.OpenSharedState(ctx, resolved)
	clearErr := shared.ClearBranchSeat(ctx, socket)
	if clearErr == nil {
		clearErr = spawn.DeleteTmuxSeat(ctx, shared, socket)
	}
	closeErr := shared.Close()
	if clearErr != nil || closeErr != nil {
		return fmt.Errorf("clear handles after ending %s: %w", socket, errors.Join(clearErr, closeErr))
	}
	return nil
}

// endTmux is the one tmux end: KillServer on the chat's pane, which returns nil
// only once tmux says the server is dead. An unresponsive server is not proof
// of an end.
func endTmux(ctx context.Context, chat headless.Chat, values paths.Values) error {
	if chat.Socket == "" {
		return fmt.Errorf("chat %s has neither a Herdr seat nor a tmux socket to end", chat.ID)
	}
	if err := KillServer(ctx, values, chat.Socket, chat.Pane); err != nil {
		return fmt.Errorf("end tmux server %s: %w", chat.Socket, err)
	}
	return nil
}
