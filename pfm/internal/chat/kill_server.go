package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// KillServer ends one chat's tmux server and removes its socket, crumbs, and
// branch-seat marker.
func KillServer(ctx context.Context, resolved paths.Values, socket string) (err error) {
	if filepath.IsAbs(socket) {
		return errors.New("native Herdr end/deactivate/reboot is not supported until phase 2")
	}
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

// EndResolved ends a resolved live chat without re-resolving its identity.
func EndResolved(ctx context.Context, chat headless.Chat, stdout, stderr io.Writer, runtime *pfmconfig.Runtime) int {
	if chat.Backend == "herdr" {
		fmt.Fprintln(stderr, "pfm chat end: native Herdr end is not supported until phase 2")
		return 2
	}
	var runtimes []pfmconfig.Runtime
	if runtime != nil {
		runtimes = append(runtimes, *runtime)
	}
	socketPath, err := paths.SocketPath(chat.Socket)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat end: %v\n", err)
		return 1
	}
	command := pfmtmux.Exec(ctx, "", socketPath, "kill-server")
	if output, err := command.CombinedOutput(); err != nil {
		fmt.Fprintf(stderr, "pfm chat end: %v: %s\n", err, strings.TrimSpace(string(output)))
		return 1
	}
	// The seat is dead, so its prompt file is litter. Cleanup is best-effort:
	// the end itself succeeded and a prompt-file failure must remain visible
	// without changing that result.
	if endRuntime, err := pfmconfig.OptionalRuntime(runtimes); err != nil {
		fmt.Fprintf(stderr, "pfm chat end: WARNING: could not resolve paths to remove its role prompt: %v\n", err)
	} else if err := agentrole.RemoveSeatPrompt(endRuntime.Paths.SIDDir, chat.Socket, chat.Pane); err != nil {
		fmt.Fprintf(stderr, "pfm chat end: WARNING: could not remove role prompt: %v\n", err)
	}
	fmt.Fprintf(stdout, "ended %s\n", chat.ID)
	return 0
}
