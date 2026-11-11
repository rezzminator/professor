package gather

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// HerdrShield is the herdr-shield door over proc. One shield serves every
// proof of one pass (a listing, a painter tick): its listing of a socket
// answers all of them, so herdr-shield runs once per socket, not once a seat.
func HerdrShield(proc ProcFS) *herdr.Shield {
	return &herdr.Shield{Runner: deps.RealRunner{}, Proc: shieldProc{proc}}
}

type shieldProc struct{ ProcFS }

func (proc shieldProc) Stat(pid int) (herdr.ProcStat, error) {
	stat, err := proc.ProcFS.Stat(pid)
	return herdr.ProcStat{Start: stat.StartTime, Group: stat.Group, TerminalGroup: stat.TerminalGroup}, err
}

// ResolveHerdrSeat proves and persists the current address using the jail-aware
// process reader. Callers must refuse mutation on either an error or !owned.
func ResolveHerdrSeat(
	ctx context.Context,
	values paths.Values,
	seat fleetdb.Seat,
) (fleetdb.Seat, bool, error) {
	return ResolveHerdrSeatWith(ctx, values, nil, seat)
}

// ResolveHerdrSeatWith is ResolveHerdrSeat reading herdr-shield through
// shield, so the proofs one pass makes share its listings; nil reads through a
// shield of this proof alone.
func ResolveHerdrSeatWith(
	ctx context.Context,
	values paths.Values,
	shield *herdr.Shield,
	seat fleetdb.Seat,
) (result fleetdb.Seat, owned bool, returnErr error) {
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	proc := NewProcFS(values.ProcRoot)
	if shield == nil {
		shield = HerdrShield(proc)
	}
	return proveHerdrSeatSnapshot(ctx, db, proc, shield, seat, nil, false)
}

// herdrSeatPane is the pane of panes seat lives in: the one holding its saved
// terminal (its saved pane when it has none), else, for a seat whose saved
// supervisor birth proveHerdrSeatSnapshot proved alive (HerdrHostDead), the
// pane herdrShieldMovedPane follows through shield. Nil when neither is listed;
// a seat with no saved birth, which no proof tells live, never asks herdr-shield.
func herdrSeatPane(
	ctx context.Context,
	proc ProcFS,
	shield *herdr.Shield,
	seat fleetdb.Seat,
	panes []herdr.Pane,
) (*herdr.Pane, error) {
	var candidate *herdr.Pane
	for i := range panes {
		pane := &panes[i]
		matches := pane.PaneID == seat.HerdrPane
		if seat.HerdrTerminalID != "" {
			matches = pane.TerminalID == seat.HerdrTerminalID
		}
		if matches {
			if candidate != nil {
				return nil, fmt.Errorf("ownership pane.list: duplicate terminal identity")
			}
			candidate = pane
		}
	}
	if candidate != nil || seat.SeatHostPID <= 0 || seat.SeatHostStart == 0 {
		return candidate, nil
	}
	moved, err := herdrShieldMovedPane(ctx, proc, shield, seat, panes)
	if err != nil {
		return nil, fmt.Errorf("seat %s ownership unproved: %w", seat.SeatID, err)
	}
	return moved, nil
}

// herdrShieldMovedPane is the pane a `herdr-shield attach` moved seat's chat
// to: the pane bound to the running holder whose shell is the nearest such
// shell above the seat's supervisor, verified by start time. Nil when no
// holder's shell runs the supervisor or its pane is not in panes.
func herdrShieldMovedPane(
	ctx context.Context,
	proc ProcFS,
	shield *herdr.Shield,
	seat fleetdb.Seat,
	panes []herdr.Pane,
) (*herdr.Pane, error) {
	holders, err := shield.Running(ctx, seat.HerdrSocket)
	if err != nil {
		return nil, fmt.Errorf("terminal not present in pane.list; follow a moved herdr-shield holder: %w", err)
	}
	if len(holders) == 0 {
		return nil, nil
	}
	seen := map[int]bool{}
	for pid := seat.SeatHostPID; pid > 1 && !seen[pid]; {
		seen[pid] = true
		stat, err := proc.Stat(pid)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf(
				"terminal not present in pane.list; follow a moved herdr-shield holder: read supervisor ancestry %d: %w",
				pid,
				err,
			)
		}
		for index := range holders {
			holder := &holders[index]
			if holder.Child.PID != pid || holder.Child.Start != stat.StartTime {
				continue
			}
			for i := range panes {
				if panes[i].PaneID == holder.Binding.Pane {
					return &panes[i], nil
				}
			}
			return nil, nil
		}
		pid = stat.ParentPID
	}
	return nil, nil
}
