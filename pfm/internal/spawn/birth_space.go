package spawn

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
)

// BirthSpace is the one inheritance of a new chat's space, asked by every door
// a new chat is born through before its first placement: the space chosen, else
// that of the live chat holding the launching Herdr pane (choice.HerdrPane),
// else the launching pane's workspace when its label is a space's
// (place.Fleet.BirthSpace). A space it finds is recorded for born, and choice's
// fleet scan, run once here, files born under it, so the placement that
// follows puts the chat in the space's workspace. A birth outside Herdr, or a
// resume, never asks: a resumed chat keeps its own recorded space. Its error
// is a warning the door prints, never a refused birth: the chat is born in its
// folder's place, as before spaces were inherited.
func BirthSpace(
	ctx context.Context,
	values paths.Values,
	choice *BackendChoice,
	born, chosen string,
) (space string, returnErr error) {
	if choice.Backend != BackendHerdr || choice.Places == nil || born == "" {
		return "", nil
	}
	defer func() {
		if returnErr != nil {
			returnErr = fmt.Errorf("chat %s is born in its folder's place, not in the space it was started from: %w",
				born, returnErr)
		}
	}()
	fleet, err := choice.Places(ctx)
	if err != nil {
		return "", fmt.Errorf("scan the fleet for chat %s's space: %w", born, err)
	}
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	parent, workspace := "", ""
	if chosen == "" && choice.HerdrSocket != "" && choice.HerdrPane != "" {
		seat, _, err := state.SeatByPane(ctx, choice.HerdrSocket, choice.HerdrPane)
		if err != nil {
			return "", fmt.Errorf("read the chat launching chat %s: %w", born, err)
		}
		if parent = seat.SessionID; parent == "" {
			if workspace, err = herdr.PaneWorkspaceLabel(ctx, choice.HerdrSocket, choice.HerdrPane); err != nil {
				return "", fmt.Errorf("read the workspace launching chat %s: %w", born, err)
			}
		}
	}
	filed := *fleet
	if space = fleet.BirthSpace(chosen, parent, workspace); space != "" {
		if err := state.SetSpace(ctx, born, space, clock.Real.Now().Unix()); err != nil {
			return "", err
		}
		filed.Spaces = maps.Clone(fleet.Spaces)
		if filed.Spaces == nil {
			filed.Spaces = map[string]string{}
		}
		filed.Spaces[born] = space
	}
	choice.Places = func(context.Context) (*place.Fleet, error) { return &filed, nil }
	return space, nil
}
