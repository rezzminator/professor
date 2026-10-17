package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// renameHerdr follows successful engine /rename delivery. Persist only after
// both visible labels agree; every partial failure is returned to the caller.
func renameHerdr(ctx context.Context, target headless.Chat, name string, values paths.Values) (returnErr error) {
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	seat, found, err := state.Seat(ctx, target.SeatID)
	if err != nil {
		return fmt.Errorf("read rename seat: %w", err)
	}
	if !found || seat.Backend != spawn.BackendHerdr || seat.HerdrSocket != target.Socket ||
		seat.HerdrPane != target.Pane {
		return fmt.Errorf("native rename target %s no longer matches its durable seat", target.SeatID)
	}
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
		before := seat
		seat.Name = name
		if err := state.UpdateSeat(ctx, before, seat); err != nil {
			return fmt.Errorf("persist name after Herdr pane and agent rename: %w", err)
		}
		return nil
	}
	return errors.New("herdr agent rename exhausted 100 unique name attempts; pane label changed, seat name unchanged")
}
