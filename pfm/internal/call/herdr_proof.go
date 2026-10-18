package call

import (
	"context"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
		if owner != nil {
			return target, fmt.Errorf("herdr pane %s ownership unproved: multiple durable owners", target.Pane)
		}
		owner = seat
	}
	if owner == nil {
		return target, fmt.Errorf("herdr pane %s ownership unproved: no durable owner", target.Pane)
	}
	if owner.HerdrSocket != target.SocketPath || target.Engine != "" && owner.Engine != target.Engine {
		return target, fmt.Errorf("herdr pane %s ownership unproved: durable owner changed", target.Pane)
	}
	proved, owned, err := gather.ResolveHerdrSeat(ctx, values, *owner)
	if err != nil {
		return target, fmt.Errorf("herdr seat %s ownership unproved: %w", owner.SeatID, err)
	}
	if !owned {
		return target, fmt.Errorf(
			"herdr seat %s ownership unproved: supervisor retired or unbound pane absent",
			owner.SeatID,
		)
	}
	target.SeatID = proved.SeatID
	target.Pane, target.SocketPath = proved.HerdrPane, proved.HerdrSocket
	target.HerdrWorkspace, target.Engine = proved.HerdrWorkspace, proved.Engine
	return target, nil
}

// ProveHerdrPane refuses stale observations rather than applying their data to
// a different canonical pane. Callers with retargetable input use ProveHerdrTarget.
func ProveHerdrPane(ctx context.Context, socket, pane string) error {
	proved, err := ProveHerdrTarget(ctx, Target{SocketPath: socket, Pane: pane})
	if err != nil {
		return err
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
