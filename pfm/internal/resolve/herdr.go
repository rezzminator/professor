package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// identifyHerdr verifies the persisted address against the caller's process
// ancestry. Inherited HERDR variables alone cannot confer a seat identity.
func (identifier *Whoami) identifyHerdr(ctx context.Context) (identity Identity, found bool, returnErr error) {
	env := identifier.environment
	if env.HerdrSocket == "" || env.HerdrPane == "" {
		return identity, false, nil
	}
	values, err := paths.Resolve()
	if err != nil {
		return identity, false, err
	}
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	seat, found, err := state.SeatByPane(ctx, env.HerdrSocket, env.HerdrPane)
	if err != nil || !found {
		return identity, false, err
	}
	if seat.State != "live" {
		return identity, false, nil
	}
	body, err := herdr.SeatRequest(
		ctx,
		seat.HerdrSocket,
		"pane.process_info",
		map[string]string{"pane_id": seat.HerdrPane},
	)
	if err != nil {
		return identity, false, fmt.Errorf("verify Herdr seat %s: %w", seat.SeatID, err)
	}
	var result struct {
		Info struct {
			Pane      string `json:"pane_id"`
			Processes []struct {
				PID   int    `json:"pid"`
				Argv0 string `json:"argv0"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return identity, false, fmt.Errorf("decode Herdr seat process: %w", err)
	}
	if result.Info.Pane != seat.HerdrPane {
		return identity, false, fmt.Errorf(
			"herdr process response for %s returned pane %q",
			seat.HerdrPane,
			result.Info.Pane,
		)
	}
	if result.Info.Processes == nil {
		return identity, false, fmt.Errorf("herdr process response for %s has no foreground_processes", seat.HerdrPane)
	}
	for _, pid := range identifier.ancestors() {
		for _, process := range result.Info.Processes {
			if process.PID == pid {
				session := seat.Name
				if session == "" {
					session = seat.SeatID
				}
				return Identity{
					Backend:        "herdr",
					SeatID:         seat.SeatID,
					HerdrWorkspace: seat.HerdrWorkspace,
					Session:        session,
					SocketPath:     seat.HerdrSocket,
					SocketName:     filepath.Base(seat.HerdrSocket),
					Pane:           seat.HerdrPane,
					Engine:         seat.Engine,
					ID:             seat.SessionID,
					Source:         "herdr-ancestry",
				}, true, nil
			}
		}
	}
	return identity, false, nil
}
