package gather

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// herdrSeatPane is the pane of panes seat lives in: the one holding its saved
// terminal (its saved pane when it has none), else, for a seat with a saved
// supervisor, the pane herdrShieldMovedPane follows. Nil when neither is listed.
func herdrSeatPane(ctx context.Context, proc ProcFS, seat fleetdb.Seat, panes []herdr.Pane) (*herdr.Pane, error) {
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
	if candidate != nil || seat.SeatHostPID <= 0 {
		return candidate, nil
	}
	moved, err := herdrShieldMovedPane(ctx, proc, seat, panes)
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
	seat fleetdb.Seat,
	panes []herdr.Pane,
) (*herdr.Pane, error) {
	holders, err := HerdrShield(proc).Running(ctx, seat.HerdrSocket)
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
