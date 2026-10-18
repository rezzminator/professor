package reload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func (n *nativeReload) seat(ctx context.Context) (fleetdb.Seat, error) {
	seat, found, err := n.db.SeatByPane(ctx, n.request.SocketPath, n.request.Pane)
	if err != nil {
		return seat, fmt.Errorf("resolve native reload seat: %w", err)
	}
	if !found && n.request.SessionID != "" {
		seat, found, err = n.db.SeatBySession(ctx, n.request.SessionID)
	}
	if err != nil {
		return seat, fmt.Errorf("resolve native reload session: %w", err)
	}
	if !found || seat.Backend != "herdr" || seat.HerdrSocket != n.request.SocketPath {
		return seat, errors.New("native reload seat does not match the explicit socket and pane")
	}
	values, err := paths.Resolve()
	if err != nil {
		return seat, fmt.Errorf("native reload proof paths: %w", err)
	}
	proved, owned, err := gather.ResolveHerdrSeat(ctx, values, seat)
	if err != nil {
		return seat, fmt.Errorf("prove native reload supervisor: %w", err)
	}
	if !owned {
		return seat, errors.New("native reload supervisor is dead")
	}
	if n.owner.SeatID == "" && n.request.Pane != seat.HerdrPane && n.request.Pane != proved.HerdrPane {
		body, err := herdr.SeatRequest(
			ctx,
			proved.HerdrSocket,
			"pane.get",
			map[string]string{herdrPaneKey: n.request.Pane},
		)
		if err != nil {
			return seat, fmt.Errorf("resolve native reload caller alias: %w", err)
		}
		var alias struct {
			Type string `json:"type"`
			Pane struct {
				ID        string `json:"pane_id"`
				Terminal  string `json:"terminal_id"`
				Workspace string `json:"workspace_id"`
			} `json:"pane"`
		}
		if err := json.Unmarshal(body, &alias); err != nil {
			return seat, fmt.Errorf("decode native reload caller alias: %w", err)
		}
		if alias.Type != "pane_info" || alias.Pane.ID != proved.HerdrPane ||
			alias.Pane.Terminal != proved.HerdrTerminalID ||
			alias.Pane.Workspace != proved.HerdrWorkspace {
			return seat, errors.New("native reload explicit pane is not the proved seat")
		}
	}
	if n.owner.SeatID != "" &&
		(n.owner.SeatID != proved.SeatID || n.owner.HerdrTerminalID != proved.HerdrTerminalID || n.owner.SeatHostPID != proved.SeatHostPID || n.owner.SeatHostStart != proved.SeatHostStart || n.owner.SeatHostBirth != proved.SeatHostBirth) {
		return seat, errors.New("native reload seat supervisor binding changed")
	}
	seat = proved
	n.owner = proved
	n.request.Pane = seat.HerdrPane
	n.target.Pane = seat.HerdrPane
	if seat.Engine != string(n.request.Engine) {
		return seat, fmt.Errorf("native reload seat engine %q differs from requested %q", seat.Engine, n.request.Engine)
	}
	if seat.EndPending {
		return seat, errors.New("native reload seat end is pending")
	}
	if seat.State != "live" && seat.State != nativeReloadFailed {
		return seat, fmt.Errorf("native reload seat is %s", seat.State)
	}
	return seat, nil
}

// refreshTarget validates the original supervisor, not a new occupant at its address.
func (n *nativeReload) refreshTarget(ctx context.Context) error {
	current, found, err := n.db.Seat(ctx, n.owner.SeatID)
	if err != nil {
		return fmt.Errorf("read native reload owner: %w", err)
	}
	if !found || current.HerdrSocket != n.owner.HerdrSocket || current.HerdrTerminalID != n.owner.HerdrTerminalID ||
		current.SeatHostPID != n.owner.SeatHostPID ||
		current.SeatHostStart != n.owner.SeatHostStart ||
		current.SeatHostBirth != n.owner.SeatHostBirth ||
		current.Engine != n.owner.Engine ||
		current.EndPending {
		return errors.New("native reload seat supervisor binding changed")
	}
	values, err := paths.Resolve()
	if err != nil {
		return fmt.Errorf("native reload proof paths: %w", err)
	}
	proved, owned, err := gather.ResolveHerdrSeat(ctx, values, current)
	if err != nil {
		return fmt.Errorf("reprove native reload target: %w", err)
	}
	if !owned {
		return errors.New("native reload supervisor is dead")
	}
	n.target.Pane = proved.HerdrPane
	n.request.Pane = proved.HerdrPane
	return nil
}

func (n *nativeReload) sendKey(ctx context.Context, key string) error {
	if err := n.refreshTarget(ctx); err != nil {
		return err
	}
	return n.client.SendKey(ctx, n.target, key)
}

func (n *nativeReload) sendLiteral(ctx context.Context, text string) error {
	if err := n.refreshTarget(ctx); err != nil {
		return err
	}
	return n.client.SendLiteral(ctx, n.target, text)
}
