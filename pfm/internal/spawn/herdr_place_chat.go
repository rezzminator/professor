package spawn

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
)

// PlaceHerdrChat is ReconcileHerdr for the one live native chat id on socket,
// the chat that just recorded a space: it goes where placementOf says, every
// other chat stays where it sits, and the place it was filed under comes back
// (the zero Place, nothing moved, for a chat filed under no place, as
// moveMisplacedHerdr skips one). A chat that is no such seat is an error, never a quiet success.
// The chat's move follows (placeHerdrSeat): when its pane is the one Herdr has
// focused, the view goes with it, and followed says so.
func PlaceHerdrChat(ctx context.Context, values paths.Values, socket string, fleet place.Fleet, id string) (
	target place.Place, followed bool, returnErr error,
) {
	unlock, err := lockHerdrPlaces(ctx, values, socket)
	if err != nil {
		return place.Place{}, false, err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	listed, err := listHerdrWorkspaces(ctx, socket)
	if err != nil {
		return place.Place{}, false, err
	}
	for position := range fleet.Rows {
		row := fleet.Rows[position]
		if row.ID == id && row.Backend == BackendHerdr && row.Kind.IsLiveSeat() && row.Socket == socket &&
			row.SeatID != "" {
			if target = fleet.Of(row.CWD, id); target.Root == "" {
				return target, false, nil // filed under no place: no workspace of its own to move into
			}
			followed, err = placeHerdrSeat(ctx, values, socket, "", true, fleet, row, target, &listed)
			if errors.Is(err, errHerdrSeatUnplaced) {
				err = fmt.Errorf("chat %s: %w", id, err)
			}
			return target, followed, err
		}
	}
	return place.Place{}, false, fmt.Errorf("chat %s is not a live native Herdr chat on %s", id, socket)
}

// herdrPaneFocused reads whether pane is the one pane Herdr has focused, the
// pane its attached clients show. A pane the list lacks, or an entry without
// its focus field, is an error, never an unfocused pane.
func herdrPaneFocused(ctx context.Context, socket, pane string) (bool, error) {
	panes, err := herdr.ListPanes(ctx, socket)
	if err != nil {
		return false, fmt.Errorf("read Herdr's focus before moving pane %s: %w", pane, err)
	}
	for index := range panes {
		if panes[index].PaneID != pane {
			continue
		}
		if panes[index].Focused == nil {
			return false, fmt.Errorf("herdr pane.list does not say whether pane %s is focused", pane)
		}
		return *panes[index].Focused, nil
	}
	return false, fmt.Errorf("pane %s is not in Herdr's pane list", pane)
}
