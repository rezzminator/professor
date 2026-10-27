package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// renameHerdr follows successful engine /rename delivery. Persist only after
// the pane label, agent name and tab label agree under herdr.LabelSoleTab's
// rule; every partial failure is returned to the caller and remains retryable.
func renameHerdr(ctx context.Context, target headless.Chat, name string, values paths.Values) (returnErr error) {
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	seat, found, err := state.Seat(ctx, target.SeatID)
	if err != nil {
		return fmt.Errorf("read rename seat: %w", err)
	}
	if !found || seat.Backend != spawn.BackendHerdr || seat.HerdrSocket != target.Socket ||
		(target.ID != "" && seat.SessionID != target.ID) {
		return fmt.Errorf("native rename target %s no longer matches its durable seat", target.SeatID)
	}
	unlock, err := fleetdb.LockSeat(ctx, values, fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID))
	if err != nil {
		return fmt.Errorf("lock rename seat: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	seat, found, err = state.Seat(ctx, target.SeatID)
	if err != nil {
		return fmt.Errorf("reread rename seat: %w", err)
	}
	if !found || seat.EndPending {
		return fmt.Errorf("native rename seat %s ownership changed or end is pending", target.SeatID)
	}
	seat, owned, err := gather.ProveHerdrSeat(ctx, state, gather.NewProcFS(values.ProcRoot), seat)
	if err != nil {
		return fmt.Errorf("prove rename seat: %w", err)
	}
	if !owned {
		return fmt.Errorf("native rename seat %s supervisor is dead", seat.SeatID)
	}
	return renameNativeOwned(ctx, state, seat, name)
}

// RenameHerdrOwned updates the pane, agent, durable name and sole-tab label.
// The caller must already hold the conversation/bridge locks and have proved
// the seat's current terminal and supervisor. Lifecycle hooks use this same
// naming path as DeliverName, without typing into the user's prompt.
func RenameHerdrOwned(ctx context.Context, state *fleetdb.Store, seat fleetdb.Seat, name string) error {
	return renameNativeOwned(ctx, state, seat, name)
}

// renameNativeOwned also serves explicit restore, whose authority is the checked
// new caller pane after positive old-host death, before its new host can start.
func renameNativeOwned(ctx context.Context, state *fleetdb.Store, seat fleetdb.Seat, name string) error {
	if err := naming.ValidateLabel(name); err != nil {
		return err
	}
	target := headless.Chat{SeatID: seat.SeatID, Socket: seat.HerdrSocket, Pane: seat.HerdrPane}
	body, err := herdr.SeatRequest(
		ctx,
		target.Socket,
		"pane.rename",
		map[string]string{"pane_id": target.Pane, "label": name},
	)
	if err != nil {
		return fmt.Errorf("rename Herdr pane after /rename delivery: %w", err)
	}
	var pane struct {
		Pane struct {
			ID    string `json:"pane_id"`
			Label string `json:"label"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(body, &pane); err != nil {
		return fmt.Errorf("decode Herdr pane rename: %w", err)
	}
	if pane.Pane.ID != target.Pane || pane.Pane.Label != name {
		return fmt.Errorf("herdr pane rename returned an unexpected acknowledgement: %s", body)
	}
	taken := make(map[string]bool)
	for attempt := 0; attempt < 100; attempt++ {
		slug := herdr.AgentName(name, taken)
		body, err := herdr.SeatRequest(
			ctx,
			target.Socket,
			"agent.rename",
			map[string]string{"target": target.Pane, "name": slug},
		)
		if err != nil {
			var refusal *herdr.Refusal
			if errors.As(err, &refusal) && refusal.Code == "agent_name_taken" {
				taken[slug] = true
				continue
			}
			return fmt.Errorf("rename Herdr agent after pane rename: %w", err)
		}
		var agent struct {
			Agent struct {
				Pane string `json:"pane_id"`
				Name string `json:"name"`
			} `json:"agent"`
		}
		if err := json.Unmarshal(body, &agent); err != nil {
			return fmt.Errorf("decode Herdr agent rename: %w", err)
		}
		if agent.Agent.Pane != target.Pane || agent.Agent.Name != slug {
			return fmt.Errorf("herdr agent rename returned an unexpected acknowledgement: %s", body)
		}
		if err := labelNativeTab(ctx, target, name); err != nil {
			return fmt.Errorf("label Herdr tab after pane and agent rename: %w", err)
		}
		before := seat
		seat.Name = name
		if err := state.UpdateSeat(ctx, before, seat); err != nil {
			return fmt.Errorf("persist name after Herdr pane, agent and tab rename: %w", err)
		}
		return nil
	}
	return errors.New("herdr agent rename exhausted 100 unique name attempts; pane label changed, seat name unchanged")
}

// labelNativeTab labels the tab the seat's pane sits in with the chat's name
// when the pane sits in it alone.
func labelNativeTab(ctx context.Context, target headless.Chat, name string) error {
	err := herdr.LabelPaneTab(ctx, target.Socket, target.Pane, name, true)
	if errors.Is(err, herdr.ErrTabNotSole) {
		// A tab the chat shares keeps the user's label: the pane title carries the name.
		return nil
	}
	return err
}
