package call

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
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
// A pane no durable seat owns is proved when it hosts a pfm tmux chat client.
func ProveHerdrPane(ctx context.Context, socket, pane string) error {
	return proveHerdrPaneOrBridge(ctx, socket, pane, ProveHerdrBridge)
}

// ProveHerdrOwnPane proves the pane this very process runs in, for closing
// or reusing that one pane alone: Herdr's pane.process_info for it must name
// this process among its foreground processes. A live process's pid names no
// other process while it runs, so this process is its own start identity;
// no other pane is proved by it.
func ProveHerdrOwnPane(ctx context.Context, socket, pane string) error {
	foreground := bridgeForeground(herdr.Call, func() uint64 { return uint64(clock.Real.Now().UnixNano()) })
	return proveOwnPane(ctx, socket, pane, os.Getpid(), foreground)
}

// ErrHerdrPaneNotOwn is ProveHerdrOwnPane's answer for a pane Herdr read
// whose foreground does not hold this process (a stale or inherited
// HERDR_PANE_ID): the pane is not this process's, and no read failed.
var ErrHerdrPaneNotOwn = errors.New("the pane's foreground does not hold this process")

func proveOwnPane(
	ctx context.Context,
	socket, pane string,
	pid int,
	foreground func(context.Context, string, string) ([]int, error),
) error {
	pids, err := foreground(ctx, socket, pane)
	if err != nil {
		return fmt.Errorf("herdr pane %s ownership unproved: %w", pane, err)
	}
	if !slices.Contains(pids, pid) {
		return fmt.Errorf(
			"herdr pane %s ownership unproved: its foreground processes %v do not hold this process %d: %w",
			pane,
			pids,
			pid,
			ErrHerdrPaneNotOwn,
		)
	}
	return nil
}

func proveHerdrPaneOrBridge(
	ctx context.Context,
	socket, pane string,
	bridge func(context.Context, string, string) (HerdrBridge, error),
) error {
	proved, err := ProveHerdrTarget(ctx, Target{SocketPath: socket, Pane: pane})
	if err != nil {
		if _, bridgeErr := bridge(ctx, socket, pane); bridgeErr != nil {
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
