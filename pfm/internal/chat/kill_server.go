package chat

import (
	"context"
	"encoding/json"
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
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// KillServer ends one chat's tmux server and removes its socket, crumbs, and
// branch-seat marker.
func KillServer(ctx context.Context, resolved paths.Values, socket string) (err error) {
	if filepath.IsAbs(socket) {
		return errors.New("KillServer requires a tmux socket name; use EndResolved for a native Herdr seat")
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
	if chat.Backend == spawn.BackendHerdr {
		effective, err := pfmconfig.RuntimeOrDefault(runtime)
		if err == nil {
			err = endHerdr(ctx, chat, effective.Paths)
		}
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat end: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "ended %s\n", chat.ID)
		return 0
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

// endHerdr validates the durable owner before closing only the selected pane.
func endHerdr(ctx context.Context, chat headless.Chat, values paths.Values) (returnErr error) {
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	seat, found, err := state.Seat(ctx, chat.SeatID)
	if err != nil {
		return fmt.Errorf("read end seat %s: %w", chat.SeatID, err)
	}
	if !found || seat.Backend != spawn.BackendHerdr || seat.SessionID != chat.ID || seat.HerdrSocket != chat.Socket ||
		seat.HerdrPane != chat.Pane {
		return fmt.Errorf("native end target %s no longer matches its durable seat", chat.SeatID)
	}
	body, err := herdr.SeatRequest(
		ctx,
		seat.HerdrSocket,
		"pane.close",
		map[string]string{nativePaneIDKey: seat.HerdrPane},
	)
	if err != nil {
		var refusal *herdr.Refusal
		if !errors.As(err, &refusal) || refusal.Code != "pane_not_found" {
			return fmt.Errorf("close Herdr pane %s: %w", seat.HerdrPane, err)
		}
	} else {
		var ack struct {
			Type string `json:"type"`
			Pane string `json:"pane_id"`
		}
		if err := json.Unmarshal(body, &ack); err != nil {
			return fmt.Errorf("decode Herdr pane close: %w", err)
		}
		if ack.Type != "ok" && (ack.Type != "pane_closed" || ack.Pane != seat.HerdrPane) {
			return fmt.Errorf("unexpected Herdr pane close acknowledgement: %s", body)
		}
	}
	if err := state.DeleteSeatRun(seat.SeatID); err != nil {
		return fmt.Errorf("remove ended seat recipe: %w", err)
	}
	if err := state.DeleteSeat(ctx, seat.SeatID); err != nil {
		return fmt.Errorf("remove ended seat: %w", err)
	}
	return nil
}
