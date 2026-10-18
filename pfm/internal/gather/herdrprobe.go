package gather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// HerdrSeat is a durable native seat and its visible probe failure, if any.
type HerdrSeat struct {
	CWD  string
	Seat fleetdb.Seat
	Err  error
}

// ProbeHerdrSeats retains every live durable seat, including unreachable ones.
// One pane listing per socket serves all its seats for this invocation.
func ProbeHerdrSeats(ctx context.Context, seats []fleetdb.Seat) []HerdrSeat {
	type snapshot struct {
		cwd map[string]string
		err error
	}
	out := make([]HerdrSeat, 0)
	sockets := make(map[string]snapshot)
	for i := range seats {
		seat := seats[i]
		if seat.Backend != "herdr" || seat.State != "live" {
			continue
		}
		current, found := sockets[seat.HerdrSocket]
		if !found {
			current.cwd, current.err = probeHerdrPanes(ctx, seat.HerdrSocket)
			if current.err != nil {
				label := "HERDR ERROR"
				var transport *net.OpError
				if errors.As(current.err, &transport) {
					label = "HERDR UNREACHABLE"
				}
				current.err = fmt.Errorf("%s %s: %w", label, seat.HerdrSocket, current.err)
			}
			sockets[seat.HerdrSocket] = current
		}
		row := HerdrSeat{Seat: seat, Err: current.err}
		if row.Err == nil {
			if cwd, present := current.cwd[seat.HerdrPane]; present {
				row.CWD = cwd
			} else {
				row.Err = fmt.Errorf(
					"HERDR ERROR %s: pane_not_found: pane %q not present in pane.list",
					seat.HerdrSocket,
					seat.HerdrPane,
				)
			}
		}
		out = append(out, row)
	}
	return out
}

// probeHerdrPanes consumes canonical pane identities and their optional CWDs
// from the same producer snapshot; it never adds per-seat round trips.
func probeHerdrPanes(ctx context.Context, socket string) (map[string]string, error) {
	body, err := herdr.SeatRequest(ctx, socket, "pane.list", struct{}{})
	if err != nil {
		return nil, err
	}
	panes, err := herdr.ParsePanes(body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Panes []struct {
			CWD           string `json:"cwd"`
			ForegroundCWD string `json:"foreground_cwd"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode pane.list CWDs: %w", err)
	}
	if result.Panes == nil {
		return nil, fmt.Errorf("pane.list: missing or null panes array")
	}
	cwd := make(map[string]string, len(panes))
	for i, pane := range panes {
		if pane.PaneID == "" {
			return nil, fmt.Errorf("pane.list: entry %d is missing pane_id", i)
		}
		if _, duplicate := cwd[pane.PaneID]; duplicate {
			return nil, fmt.Errorf("pane.list: duplicate pane_id %q", pane.PaneID)
		}
		directory := result.Panes[i].ForegroundCWD
		if directory == "" {
			directory = result.Panes[i].CWD
		}
		cwd[pane.PaneID] = directory
	}
	return cwd, nil
}
