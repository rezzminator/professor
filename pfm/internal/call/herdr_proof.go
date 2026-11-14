package call

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

var errNoHerdrOwner = errors.New("no durable owner")

// ProveHerdrTarget resolves the durable supervisor before a pane-addressed
// operation, returning its current address rather than trusting a stale target.
func ProveHerdrTarget(ctx context.Context, target Target) (Target, error) {
	values, err := paths.Resolve()
	if err != nil {
		return target, fmt.Errorf("herdr ownership unproved: resolve paths: %w", err)
	}
	seats, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		return target, fmt.Errorf("herdr ownership unproved: read durable owners: %w", err)
	}
	return proveHerdrOwner(values, seats, target, func(seat fleetdb.Seat) (fleetdb.Seat, bool, error) {
		return gather.ResolveHerdrSeat(ctx, values, seat)
	})
}

// proveHerdrTargetIn is ProveHerdrTarget over a shared state store its caller
// holds open: the durable owners and the seat proof read state.
func proveHerdrTargetIn(ctx context.Context, state *fleetdb.Store, target Target) (Target, error) {
	values, err := paths.Resolve()
	if err != nil {
		return target, fmt.Errorf("herdr ownership unproved: resolve paths: %w", err)
	}
	seats, err := state.Seats(ctx)
	if err != nil {
		return target, fmt.Errorf("herdr ownership unproved: read durable owners: %w", err)
	}
	return proveHerdrOwner(values, seats, target, func(seat fleetdb.Seat) (fleetdb.Seat, bool, error) {
		return gather.ProveHerdrSeat(ctx, state, gather.NewProcFS(values.ProcRoot), seat)
	})
}

// proveHerdrOwner proves target's one durable owner among seats, prove
// resolving the owner's current address.
func proveHerdrOwner(
	values paths.Values,
	seats []fleetdb.Seat,
	target Target,
	prove func(fleetdb.Seat) (fleetdb.Seat, bool, error),
) (Target, error) {
	var owner *fleetdb.Seat
	for i := range seats {
		seat := &seats[i]
		if seat.Backend != backendHerdr {
			continue
		}
		matches := seat.HerdrSocket == target.SocketPath && seat.HerdrPane == target.Pane
		if target.SeatID != "" {
			matches = seat.SeatID == target.SeatID
		}
		if !matches {
			continue
		}
		if target.SeatID == "" && seat.State != "live" && seat.SeatHostPID > 0 && seat.SeatHostStart != 0 {
			// A retired seat keeps its last pane for its resume recipe; once its
			// supervisor is proved dead, it no longer owns that pane.
			dead, err := gather.HerdrHostDead(gather.NewProcFS(values.ProcRoot), *seat)
			if err != nil {
				return target, fmt.Errorf("herdr pane %s ownership unproved: prove seat %s retired: %w",
					target.Pane, seat.SeatID, err)
			}
			if dead {
				continue
			}
		}
		if owner != nil {
			return target, fmt.Errorf("herdr pane %s ownership unproved: multiple durable owners", target.Pane)
		}
		owner = seat
	}
	if owner == nil {
		return target, fmt.Errorf("herdr pane %s ownership unproved: %w", target.Pane, errNoHerdrOwner)
	}
	if owner.HerdrSocket != target.SocketPath || target.Engine != "" && owner.Engine != target.Engine {
		return target, fmt.Errorf("herdr pane %s ownership unproved: durable owner changed", target.Pane)
	}
	proved, owned, err := prove(*owner)
	if err != nil {
		return target, fmt.Errorf("herdr seat %s ownership unproved: %w", owner.SeatID, err)
	}
	if !owned {
		return target, fmt.Errorf(
			"herdr seat %s ownership unproved: supervisor retired or unbound pane absent",
			owner.SeatID,
		)
	}
	target.ID = proved.SessionID
	target.SeatID = proved.SeatID
	target.Pane, target.SocketPath = proved.HerdrPane, proved.HerdrSocket
	target.HerdrWorkspace, target.Engine = proved.HerdrWorkspace, proved.Engine
	return target, nil
}

// ProveHerdrPane refuses stale observations rather than applying their data to
// a different canonical pane. Callers with retargetable input use ProveHerdrTarget.
// A pane no durable seat owns is proved when it hosts a pfm tmux chat client.
// Positive absence of both returns herdr.ErrNotFleetPane; a failed read never does.
func ProveHerdrPane(ctx context.Context, socket, pane string) error {
	return proveHerdrPaneOrBridge(ctx, socket, pane, ProveHerdrBridge)
}

// ProveHerdrPaneIn is ProveHerdrPane over a shared state store its caller
// holds open (the Herdr indicator painter's, store.Held), so a proof on every
// tick opens no database.
func ProveHerdrPaneIn(ctx context.Context, state *fleetdb.Store, socket, pane string) error {
	prove := func(ctx context.Context, target Target) (Target, error) {
		return proveHerdrTargetIn(ctx, state, target)
	}
	return proveHerdrPaneWith(ctx, socket, pane, prove, ProveHerdrBridge)
}

func proveHerdrPaneOrBridge(
	ctx context.Context,
	socket, pane string,
	bridge func(context.Context, string, string) (HerdrBridge, error),
) error {
	return proveHerdrPaneWith(ctx, socket, pane, ProveHerdrTarget, bridge)
}

func proveHerdrPaneWith(
	ctx context.Context,
	socket, pane string,
	prove func(context.Context, Target) (Target, error),
	bridge func(context.Context, string, string) (HerdrBridge, error),
) error {
	proved, err := prove(ctx, Target{SocketPath: socket, Pane: pane})
	if err != nil {
		if _, bridgeErr := bridge(ctx, socket, pane); bridgeErr != nil {
			if errors.Is(err, errNoHerdrOwner) && errors.Is(bridgeErr, errNoHerdrBridge) {
				return errors.Join(herdr.ErrNotFleetPane, err, bridgeErr)
			}
			return errors.Join(err, bridgeErr)
		}
		return nil
	}
	if proved.SocketPath != socket || proved.Pane != pane {
		return fmt.Errorf(
			"herdr pane %s ownership unproved: canonical address changed to %s; refresh observation",
			pane,
			proved.Pane,
		)
	}
	return nil
}

// ProveHerdrBridge proves the Herdr pane hosts a pfm tmux chat client: a
// foreground tmux client of a server under pfm's tmux directory, attached to a
// session holding pfm's Herdr record, whose start identity held across the proof.
func ProveHerdrBridge(ctx context.Context, socket, pane string) (HerdrBridge, error) {
	values, err := paths.Resolve()
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("herdr pane %s bridge ownership unproved: resolve paths: %w", pane, err)
	}
	return bridgeProver{
		values: values,
		proc:   gather.NewProcFS(values.ProcRoot),
		foreground: bridgeForeground(
			herdr.Call,
			func() uint64 { return uint64(clock.Real.Now().UnixNano()) },
		),
		tmux: bridgeTmux,
	}.prove(ctx, socket, pane)
}

// HerdrEngine reports whether seat's engine is in its pane's foreground, from
// Herdr's pane.process_info.
func HerdrEngine(ctx context.Context, seat fleetdb.Seat) (bool, error) {
	processes, err := herdr.ReadPaneProcesses(ctx, seat.HerdrSocket, seat.HerdrPane)
	if err != nil {
		return false, fmt.Errorf("read Herdr foreground processes of %s: %w", seat.HerdrPane, err)
	}
	return herdr.EngineForeground(processes, seat.HerdrPane, pfmengine.ID(seat.Engine))
}
