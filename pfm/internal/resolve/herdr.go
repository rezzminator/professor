package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	nativeHerdrBackend   = "herdr"
	nativeHerdrLiveState = "live"
)

type herdrClaim struct {
	Identity
	terminal string
}

// proveHerdr proves the process location before consulting any seat rows.
func (identifier *Whoami) proveHerdr(ctx context.Context, chain []int) (herdrClaim, int, string, error) {
	env := identifier.environment
	claim := herdrClaim{}
	if env.HerdrSocket == "" || env.HerdrPane == "" {
		return claim, -1, "", nil
	}
	failure := func(err error) (herdrClaim, int, string, error) {
		var refusal *herdr.Refusal
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) ||
			errors.As(err, &refusal) && refusal.Code == "pane_not_found" {
			return claim, -1, fmt.Sprintf("herdr %s %s: %v", env.HerdrSocket, env.HerdrPane, err), nil
		}
		return claim, -1, "", fmt.Errorf("look up the caller: herdr pane %s: %w", env.HerdrPane, err)
	}
	body, err := herdr.SeatRequest(ctx, env.HerdrSocket, "pane.get", map[string]string{"pane_id": env.HerdrPane})
	if err != nil {
		return failure(err)
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
		return failure(fmt.Errorf("decode pane identity: %w", err))
	}
	pane := canonical.Pane
	if canonical.Type != "pane_info" || pane.ID == "" || pane.Workspace == "" || pane.Terminal == "" ||
		!strings.HasPrefix(pane.ID, pane.Workspace+":") {
		return failure(fmt.Errorf("malformed pane identity: %s", body))
	}
	body, err = herdr.SeatRequest(ctx, env.HerdrSocket, "pane.process_info", map[string]string{"pane_id": pane.ID})
	if err != nil {
		return failure(err)
	}
	pids, err := herdr.ParseProcessInfo(body)
	if err != nil {
		return failure(err)
	}
	var result struct {
		Info struct {
			Pane string `json:"pane_id"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return failure(fmt.Errorf("decode pane process identity: %w", err))
	}
	if result.Info.Pane != pane.ID {
		return failure(fmt.Errorf("process response returned pane %q, want %q", result.Info.Pane, pane.ID))
	}
	claim = herdrClaim{
		Identity: Identity{
			Backend:        nativeHerdrBackend,
			SocketPath:     env.HerdrSocket,
			SocketName:     filepath.Base(env.HerdrSocket),
			Pane:           pane.ID,
			HerdrWorkspace: pane.Workspace,
			Source:         "herdr-ancestry",
		},
		terminal: pane.Terminal,
	}
	for _, pid := range pids {
		if pid <= 0 {
			return failure(fmt.Errorf("malformed foreground process pid %d", pid))
		}
	}
	nearest := -1
	for index, ancestor := range chain {
		for _, pid := range pids {
			if pid != ancestor {
				continue
			}
			if nearest < 0 || index < nearest {
				nearest = index
			}
			if pid == identifier.selfPID {
				claim.Fronted = true
			}
		}
	}
	if nearest < 0 {
		return claim, -1, fmt.Sprintf("herdr %s %s: foreground has no ancestor", env.HerdrSocket, pane.ID), nil
	}
	return claim, nearest, "", nil
}

// identifyHerdr resolves a proven pane to its one durable seat.
func (identifier *Whoami) identifyHerdr(ctx context.Context, claim herdrClaim) (identity Identity, returnErr error) {
	identity = claim.Identity
	values, err := paths.Resolve()
	if err != nil {
		return identity, fmt.Errorf("look up the caller: resolve state paths: %w", err)
	}
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close caller seat state: %w", err))
		}
	}()
	seat, found, err := state.SeatByPane(ctx, identifier.environment.HerdrSocket, identifier.environment.HerdrPane)
	if err != nil {
		return identity, fmt.Errorf("look up the caller: find Herdr seat: %w", err)
	}
	if found && seat.HerdrTerminalID != "" && seat.HerdrTerminalID != claim.terminal {
		found = false
	}
	var unregistered *fleetdb.Seat
	seats, err := state.Seats(ctx)
	if err != nil {
		return identity, fmt.Errorf("look up the caller: find Herdr terminal: %w", err)
	}
	for i := range seats {
		candidate := &seats[i]
		if found && candidate.SeatID == seat.SeatID {
			continue
		}
		if candidate.Backend != nativeHerdrBackend || candidate.HerdrSocket != identity.SocketPath ||
			candidate.HerdrTerminalID != claim.terminal {
			continue
		}
		if candidate.State != nativeHerdrLiveState {
			if candidate.SeatHostPID == 0 {
				if candidate.HerdrPane == identity.Pane {
					unregistered = candidate
				}
				continue
			}
			dead, err := gather.HerdrHostDead(gather.NewProcFS(values.ProcRoot), *candidate)
			if err != nil {
				return identity, fmt.Errorf("prove historical caller seat %s retired: %w", candidate.SeatID, err)
			}
			if dead {
				continue
			}
		}
		if found {
			return identity, fmt.Errorf("%w: herdr caller terminal has multiple durable owners", ErrAmbiguous)
		}
		seat, found = *candidate, true
	}
	if !found && unregistered != nil {
		return identity, fmt.Errorf(
			"%w: herdr seat %s is %s",
			ErrNotRegistered,
			unregistered.SeatID,
			unregistered.State,
		)
	}
	if !found || seat.Backend != nativeHerdrBackend ||
		seat.HerdrTerminalID != "" && seat.HerdrTerminalID != claim.terminal {
		return identity, fmt.Errorf("%w: herdr pane %s has no seat", ErrNotRegistered, identity.Pane)
	}
	if seat.State != nativeHerdrLiveState {
		return identity, fmt.Errorf("%w: herdr seat %s is %s", ErrNotRegistered, seat.SeatID, seat.State)
	}
	proved, owned, err := gather.ProveHerdrSeat(ctx, state, gather.NewProcFS(values.ProcRoot), seat)
	if err != nil {
		return identity, fmt.Errorf("look up the caller: prove Herdr caller supervisor: %w", err)
	}
	if !owned {
		return identity, fmt.Errorf("look up the caller: herdr caller seat %s supervisor is dead", seat.SeatID)
	}
	if proved.HerdrTerminalID != claim.terminal || proved.HerdrPane != identity.Pane ||
		proved.HerdrWorkspace != identity.HerdrWorkspace {
		return identity, fmt.Errorf("look up the caller: herdr caller pane changed during supervisor proof")
	}
	identity.SeatID, identity.Session = proved.SeatID, proved.Name
	if identity.Session == "" {
		// The durable handle call and status resolve; a bare seat id is no address.
		identity.Session = SeatPrefix + proved.SeatID
	}
	identity.Engine, identity.ID = proved.Engine, proved.SessionID
	return identity, nil
}
