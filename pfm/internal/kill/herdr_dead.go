package kill

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// nativeTarget resolves request's native seat (herdrTarget), first rewriting a request named by a dead native
// seat's own id onto the conversation id that seat's row is keyed on (deadSeatIdentity), its dead address
// dropped: the kill then routes as a kill by that conversation, so a live seat resumed under it ends with the
// hide, as a kill named by the session ends it. A self kill and a tmux address never name a native seat id.
func (manager *Manager) nativeTarget(ctx context.Context, request *Request) (Target, bool, error) {
	if request.ID != "" && !request.Self && (request.SocketName == "" || filepath.IsAbs(request.SocketName)) {
		identity, err := manager.deadSeatIdentity(ctx, request.ID)
		if err != nil {
			return Target{}, false, fmt.Errorf("resolve kill seat: %w", err)
		}
		if identity != "" {
			request.ID, request.SocketName, request.PaneID = identity, "", ""
		}
	}
	return manager.herdrTarget(ctx, *request)
}

// deadSeatIdentity is the conversation id a kill named by id hides when id is a dead (non-live) native seat's
// own seat id, as resolvedKillRequest names every native row: that row is keyed on its conversation
// (compose.herdrRows), so the hide lands there, never on the seat id no listing filters on. Only an exact seat
// id resolves, so a session a live seat shares never routes to a dead one. Empty when id names no such seat.
func (manager *Manager) deadSeatIdentity(ctx context.Context, id string) (string, error) {
	seat, found, err := manager.database.Shared().Seat(ctx, id)
	if err != nil || !found || seat.Backend != herdrBackend || seat.State == liveSeatState {
		return "", err
	}
	return fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID), nil
}
