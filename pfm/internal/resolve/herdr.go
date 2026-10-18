package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	nativeHerdrBackend   = "herdr"
	nativeHerdrLiveState = "live"
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
	seats, err := state.Seats(ctx)
	if err != nil {
		return identity, false, fmt.Errorf("find Herdr caller candidates: %w", err)
	}
	candidate := false
	for i := range seats {
		row := &seats[i]
		if row.Backend == nativeHerdrBackend && row.State == nativeHerdrLiveState &&
			row.HerdrSocket == env.HerdrSocket {
			candidate = true
			break
		}
	}
	if !candidate {
		return identity, false, nil
	}

	body, err := herdr.SeatRequest(ctx, env.HerdrSocket, "pane.get", map[string]string{"pane_id": env.HerdrPane})
	if err != nil {
		return identity, false, fmt.Errorf("resolve Herdr caller pane: %w", err)
	}
	var canonical struct {
		Type string `json:"type"`
		Pane struct {
			ID        string `json:"pane_id"`
			Workspace string `json:"workspace_id"`
			Terminal  string `json:"terminal_id"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(body, &canonical); err != nil {
		return identity, false, fmt.Errorf("decode Herdr caller pane: %w", err)
	}
	pane := canonical.Pane
	if canonical.Type != "pane_info" || pane.ID == "" || pane.Workspace == "" || pane.Terminal == "" ||
		!strings.HasPrefix(pane.ID, pane.Workspace+":") {
		return identity, false, fmt.Errorf("herdr caller pane has malformed identity: %s", body)
	}
	seat, found, err := state.SeatByPane(ctx, env.HerdrSocket, env.HerdrPane)
	if err != nil {
		return identity, false, fmt.Errorf("find Herdr caller seat: %w", err)
	}
	if !found || seat.HerdrTerminalID != "" && seat.HerdrTerminalID != pane.Terminal {
		found = false
		seats, err := state.Seats(ctx)
		if err != nil {
			return identity, false, fmt.Errorf("find Herdr caller terminal: %w", err)
		}
		for i := range seats {
			candidate := &seats[i]
			if candidate.Backend == nativeHerdrBackend && candidate.HerdrSocket == env.HerdrSocket &&
				candidate.HerdrTerminalID == pane.Terminal {
				if candidate.State != nativeHerdrLiveState {
					if candidate.SeatHostPID == 0 {
						continue
					}
					dead, err := gather.HerdrHostDead(gather.NewProcFS(values.ProcRoot), *candidate)
					if err != nil {
						return identity, false, fmt.Errorf(
							"prove historical caller seat %s retired: %w",
							candidate.SeatID,
							err,
						)
					}
					if dead {
						continue
					}
				}
				if found {
					return identity, false, fmt.Errorf("herdr caller terminal has multiple durable owners")
				}
				seat = *candidate
				found = true
			}
		}
	}
	if !found || seat.Backend != nativeHerdrBackend ||
		seat.HerdrTerminalID != "" && seat.HerdrTerminalID != pane.Terminal {
		return identity, false, nil
	}
	if seat.State != nativeHerdrLiveState {
		return identity, false, nil
	}
	body, err = herdr.SeatRequest(
		ctx,
		seat.HerdrSocket,
		"pane.process_info",
		map[string]string{"pane_id": pane.ID},
	)
	if err != nil {
		return identity, false, fmt.Errorf("verify Herdr seat %s: %w", seat.SeatID, err)
	}
	if _, err := herdr.ParseProcessInfo(body); err != nil {
		return identity, false, err
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
	if result.Info.Pane != pane.ID {
		return identity, false, fmt.Errorf(
			"herdr process response for %s returned pane %q",
			seat.HerdrPane,
			result.Info.Pane,
		)
	}
	for _, pid := range identifier.ancestors() {
		for _, process := range result.Info.Processes {
			if process.PID != pid || pid <= 0 {
				continue
			}
			proved, owned, err := gather.ProveHerdrSeat(ctx, state, gather.NewProcFS(values.ProcRoot), seat)
			if err != nil {
				return identity, false, fmt.Errorf("prove Herdr caller supervisor: %w", err)
			}
			if !owned {
				return identity, false, fmt.Errorf("herdr caller seat %s supervisor is dead", seat.SeatID)
			}
			if proved.HerdrTerminalID != pane.Terminal || proved.HerdrPane != pane.ID ||
				proved.HerdrWorkspace != pane.Workspace {
				return identity, false, fmt.Errorf("herdr caller pane changed during supervisor proof")
			}
			seat = proved
			session := seat.Name
			if session == "" {
				session = seat.SeatID
			}
			return Identity{
				Backend:        seat.Backend,
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
	return identity, false, nil
}
