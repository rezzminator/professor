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

// HerdrProofs proves who owns a Herdr pane and reads what runs in it, every
// proof reading herdr-shield through Shield: the proofs of one pass (a tab
// painter tick) share its listing of a socket, so herdr-shield runs once per
// socket however many panes they prove. The zero HerdrProofs reads through a
// shield of each proof alone.
type HerdrProofs struct{ Shield *herdr.Shield }

// ProveHerdrTarget resolves the durable supervisor before a pane-addressed
// operation, returning its current address rather than trusting a stale target.
func ProveHerdrTarget(ctx context.Context, target Target) (Target, error) {
	return HerdrProofs{}.Target(ctx, target)
}

// Target is ProveHerdrTarget through proofs' shield.
func (proofs HerdrProofs) Target(ctx context.Context, target Target) (Target, error) {
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
	proved, owned, err := proofs.Seat(ctx, *owner, values)
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
	return HerdrProofs{}.Pane(ctx, socket, pane)
}

// Pane is ProveHerdrPane through proofs' shield.
func (proofs HerdrProofs) Pane(ctx context.Context, socket, pane string) error {
	return proveHerdrPaneOrBridge(ctx, proofs, socket, pane, proofs.Bridge)
}

func proveHerdrPaneOrBridge(
	ctx context.Context,
	proofs HerdrProofs,
	socket, pane string,
	bridge func(context.Context, string, string) (HerdrBridge, error),
) error {
	proved, err := proofs.Target(ctx, Target{SocketPath: socket, Pane: pane})
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
	return HerdrProofs{}.Bridge(ctx, socket, pane)
}

// Bridge is ProveHerdrBridge through proofs' shield.
func (proofs HerdrProofs) Bridge(ctx context.Context, socket, pane string) (HerdrBridge, error) {
	values, err := paths.Resolve()
	if err != nil {
		return HerdrBridge{}, fmt.Errorf("herdr pane %s bridge ownership unproved: resolve paths: %w", pane, err)
	}
	proc := gather.NewProcFS(values.ProcRoot)
	shield := proofs.Shield
	if shield == nil {
		shield = gather.HerdrShield(proc)
	}
	return bridgeProver{
		values: values,
		proc:   proc,
		foreground: bridgeForeground(
			herdr.Call,
			func() uint64 { return uint64(clock.Real.Now().UnixNano()) },
			shield,
		),
		tmux: bridgeTmux,
	}.prove(ctx, socket, pane)
}

// Seat proves and persists seat's current address (gather.ResolveHerdrSeat)
// through proofs' shield. Callers must refuse mutation on an error or !owned.
func (proofs HerdrProofs) Seat(
	ctx context.Context,
	seat fleetdb.Seat,
	values paths.Values,
) (fleetdb.Seat, bool, error) {
	return gather.ResolveHerdrSeatWith(ctx, values, proofs.Shield, seat)
}

// Engine reports whether seat's engine is in its pane's foreground, from
// Herdr's pane.process_info through proofs' shield.
func (proofs HerdrProofs) Engine(ctx context.Context, seat fleetdb.Seat) (bool, error) {
	values, err := paths.Resolve()
	if err != nil {
		return false, fmt.Errorf("read Herdr foreground processes of %s: resolve paths: %w", seat.HerdrPane, err)
	}
	shield := proofs.Shield
	if shield == nil {
		shield = gather.HerdrShield(gather.NewProcFS(values.ProcRoot))
	}
	processes, err := shield.Read(ctx, seat.HerdrSocket, seat.HerdrPane)
	if err != nil {
		return false, fmt.Errorf("read Herdr foreground processes of %s: %w", seat.HerdrPane, err)
	}
	return herdr.EngineForeground(processes, seat.HerdrPane, pfmengine.ID(seat.Engine))
}
