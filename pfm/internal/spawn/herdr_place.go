package spawn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
)

// herdrSeatLockWait bounds the wait for a chat another command holds (an open,
// a restore, a reload): that chat stays where it is until the next placement.
const herdrSeatLockWait = 2 * time.Second

// herdrPlacesLockWait bounds the wait for another placement on the same socket.
const herdrPlacesLockWait = 15 * time.Second

const (
	herdrFocusKey = "focus"
	seatLiveState = "live"
)

// herdrWorkspaces is one workspace.list: each label's workspace and each
// workspace's pane count.
type herdrWorkspaces struct {
	byLabel map[string]string
	panes   map[string]int
}

func listHerdrWorkspaces(ctx context.Context, socket string) (herdrWorkspaces, error) {
	body, err := herdr.SeatRequest(ctx, socket, "workspace.list", struct{}{})
	if err != nil {
		return herdrWorkspaces{}, fmt.Errorf("list Herdr workspaces: %w", err)
	}
	var reply struct {
		Type       string `json:"type"`
		Workspaces []struct {
			ID    string `json:"workspace_id"`
			Label string `json:"label"`
			Panes int    `json:"pane_count"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return herdrWorkspaces{}, fmt.Errorf("decode Herdr workspace list: %w", err)
	}
	if reply.Type != "workspace_list" || reply.Workspaces == nil {
		return herdrWorkspaces{}, fmt.Errorf("herdr workspace.list: unexpected result %s", body)
	}
	listed := herdrWorkspaces{byLabel: map[string]string{}, panes: map[string]int{}}
	for _, workspace := range reply.Workspaces {
		if workspace.ID == "" {
			return herdrWorkspaces{}, fmt.Errorf("herdr workspace.list: a workspace without an id in %s", body)
		}
		listed.panes[workspace.ID] = workspace.Panes
		// Two workspaces a user labelled alike: the first listed, every time.
		if _, found := listed.byLabel[workspace.Label]; !found {
			listed.byLabel[workspace.Label] = workspace.ID
		}
	}
	return listed, nil
}

// workspaceFor is the workspace target's chats sit in: the one labelled as the
// place, else one created for it, unfocused, opening in the place's root. It
// never renames, closes or duplicates a workspace.
func (listed herdrWorkspaces) workspaceFor(ctx context.Context, socket string, target place.Place) (string, error) {
	if target.Root == "" || target.Label == "" {
		return "", fmt.Errorf("herdr workspace: place %+v has no root or label", target)
	}
	if id := listed.byLabel[target.Label]; id != "" {
		return id, nil
	}
	body, err := herdr.SeatRequest(ctx, socket, "workspace.create", map[string]any{
		"cwd": target.Root, "label": target.Label, herdrFocusKey: false,
	})
	if err != nil {
		return "", fmt.Errorf("create Herdr workspace %q at %s: %w", target.Label, target.Root, err)
	}
	var reply struct {
		Type      string `json:"type"`
		Workspace struct {
			ID    string `json:"workspace_id"`
			Label string `json:"label"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return "", fmt.Errorf("herdr workspace %q may exist: decode reply: %w", target.Label, err)
	}
	if reply.Type != "workspace_created" || reply.Workspace.ID == "" || reply.Workspace.Label != target.Label {
		return "", fmt.Errorf("herdr workspace %q may exist: unexpected reply %s", target.Label, body)
	}
	listed.byLabel[target.Label] = reply.Workspace.ID
	listed.panes[reply.Workspace.ID] = 1
	return reply.Workspace.ID, nil
}

// lockHerdrPlaces serializes placement on one Herdr socket across processes,
// so two births or reconciles never both create a place's missing workspace.
func lockHerdrPlaces(ctx context.Context, values paths.Values, socket string) (func() error, error) {
	// The server-start reconcile can run before any pfm command made the state directory.
	if err := os.MkdirAll(filepath.Dir(values.StateDB), 0o700); err != nil {
		return nil, fmt.Errorf("lock Herdr placement on %s: create state directory: %w", socket, err)
	}
	lockCtx, cancel := context.WithTimeout(ctx, herdrPlacesLockWait)
	defer cancel()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(socket)))
	unlock, err := fleetdb.LockMigration(lockCtx, values.StateDB+".herdr-places-"+key)
	if err != nil {
		return nil, fmt.Errorf("lock Herdr placement on %s: %w", socket, err)
	}
	return unlock, nil
}

// ReconcileHerdr moves every live native chat on socket that sits outside its
// place's workspace into that workspace, as a new unfocused tab, creating the
// workspace when its place has none; only places with a live chat to hold get
// one. It moves only a seat it proves under the seat's lock — never a shell or
// another program's pane, never the pane skip names (the one running this
// command), never a workspace's last pane, since that would close it. Every
// chat it could not move is in the joined error; the others still move.
func ReconcileHerdr(ctx context.Context, values paths.Values, socket string, fleet place.Fleet, skip string) (
	returnErr error,
) {
	unlock, err := lockHerdrPlaces(ctx, values, socket)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	return moveMisplacedHerdr(ctx, values, socket, fleet, skip)
}

// moveMisplacedHerdr is ReconcileHerdr under a placement lock already held.
func moveMisplacedHerdr(ctx context.Context, values paths.Values, socket string, fleet place.Fleet, skip string) error {
	index := fleet.Labeled()
	var listed herdrWorkspaces
	// A scan that could not read every chat is never a fleet with nothing to move.
	failures := make([]error, 0, len(fleet.Faults))
	for _, fault := range fleet.Faults {
		failures = append(failures, fmt.Errorf("fleet scan: %s", fault))
	}
	for position := range fleet.Rows {
		row := &fleet.Rows[position]
		if row.Backend != BackendHerdr || !row.Kind.IsLiveSeat() || row.Socket != socket || row.SeatID == "" ||
			skip != "" && row.PaneID == skip {
			continue
		}
		target := index.PlaceOf(position)
		if target.Root == "" {
			continue
		}
		if listed.byLabel == nil {
			var err error
			if listed, err = listHerdrWorkspaces(ctx, socket); err != nil {
				return err
			}
		}
		if row.HerdrWorkspace != "" && listed.byLabel[target.Label] == row.HerdrWorkspace {
			continue
		}
		tabLabel := BirthTabLabel(row.Name, row.CWD)
		if err := moveHerdrSeat(ctx, values, socket, row.SeatID, skip, target, tabLabel, listed); err != nil {
			failures = append(
				failures,
				fmt.Errorf("chat %s stays out of workspace %q: %w", row.Name, target.Label, err),
			)
		}
	}
	return errors.Join(failures...)
}

// moveHerdrSeat moves one proved live seat into target's workspace and
// rewrites the seat to the pane id Herdr gives it there, through the seat's
// CAS: a pane moved across workspaces takes a new id, and every reader finds
// the chat by its seat. A seat that ended since the scan, already sits there,
// or proves to be the pane skip names stays. The workspace is created only
// once the seat proved movable, so a place gets one only for a chat it holds.
func moveHerdrSeat(
	ctx context.Context,
	values paths.Values,
	socket, seatID, skip string,
	target place.Place,
	tabLabel string,
	listed herdrWorkspaces,
) (returnErr error) {
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	seat, found, err := state.Seat(ctx, seatID)
	if err != nil {
		return fmt.Errorf("read seat %s: %w", seatID, err)
	}
	if !found || seat.Backend != BackendHerdr || seat.State != seatLiveState {
		return nil
	}
	lockCtx, cancel := context.WithTimeout(ctx, herdrSeatLockWait)
	unlock, err := fleetdb.LockSeat(lockCtx, values, fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID))
	cancel()
	if err != nil {
		return fmt.Errorf("lock seat %s: %w", seatID, err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	proved, owned, err := gather.ProveHerdrSeat(ctx, state, gather.NewProcFS(values.ProcRoot), seat)
	if err != nil {
		return fmt.Errorf("prove seat %s: %w", seatID, err)
	}
	if !owned || skip != "" && proved.HerdrPane == skip {
		return nil
	}
	if existing := listed.byLabel[target.Label]; existing != "" && proved.HerdrWorkspace == existing {
		return nil
	}
	if proved.HerdrTerminalID == "" || proved.SeatHostPID <= 0 {
		return fmt.Errorf("seat %s has no proved terminal and supervisor", seatID)
	}
	if listed.panes[proved.HerdrWorkspace] <= 1 {
		return fmt.Errorf("it is the last pane of workspace %s, which a move would close", proved.HerdrWorkspace)
	}
	workspace, err := listed.workspaceFor(ctx, socket, target)
	if err != nil {
		return err
	}
	body, err := herdr.SeatRequest(ctx, proved.HerdrSocket, "pane.move", map[string]any{
		"pane_id":     proved.HerdrPane,
		"destination": map[string]any{"type": "new_tab", "workspace_id": workspace, "label": tabLabel},
		herdrFocusKey: false,
	})
	if err != nil {
		return fmt.Errorf("herdr pane.move %s: %w", proved.HerdrPane, err)
	}
	var reply struct {
		Type string `json:"type"`
		Move struct {
			Previous string `json:"previous_pane_id"`
			Pane     struct {
				ID        string `json:"pane_id"`
				Workspace string `json:"workspace_id"`
				Terminal  string `json:"terminal_id"`
			} `json:"pane"`
		} `json:"move_result"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return fmt.Errorf("herdr pane.move %s may have moved: decode reply: %w", proved.HerdrPane, err)
	}
	moved := reply.Move.Pane
	if reply.Type != "pane_move" || reply.Move.Previous != proved.HerdrPane || moved.Workspace != workspace ||
		moved.Terminal != proved.HerdrTerminalID || !strings.HasPrefix(moved.ID, workspace+":") {
		return fmt.Errorf("herdr pane.move %s may have moved: unexpected reply %s", proved.HerdrPane, body)
	}
	listed.panes[proved.HerdrWorkspace]--
	listed.panes[workspace]++
	next := proved
	next.HerdrPane, next.HerdrWorkspace = moved.ID, moved.Workspace
	next.UpdatedAt = clock.Real.Now().Unix()
	if err := state.UpdateSeat(ctx, proved, next); err != nil {
		// A proof that ran meanwhile already learned the new address.
		current, found, readErr := state.Seat(ctx, seatID)
		if readErr == nil && found && current.HerdrPane == next.HerdrPane && current.HerdrWorkspace == workspace {
			return nil
		}
		return fmt.Errorf("pane %s moved to %s; rewrite seat %s: %w", proved.HerdrPane, moved.ID, seatID,
			errors.Join(err, readErr))
	}
	obs.Logger(ctx).InfoContext(ctx, "chat.herdr_moved", "seat", seatID, "from", proved.HerdrPane, "to", moved.ID)
	return nil
}

// placeHerdrBirth is the workspace a new tab of request's chat is born in: its
// place in fleet's, created when the place has none, under the socket's
// placement lock, after every other misplaced running chat moved into its own.
// unmoved names each chat the reconcile could not move; it never stops the
// birth.
func placeHerdrBirth(
	ctx context.Context,
	request HerdrRequest,
	fleet place.Fleet,
) (workspace string, unmoved, returnErr error) {
	socket := request.Choice.HerdrSocket
	unlock, err := lockHerdrPlaces(ctx, request.Paths, socket)
	if err != nil {
		return "", nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	unmoved = moveMisplacedHerdr(ctx, request.Paths, socket, fleet, request.Choice.HerdrPane)
	if unmoved != nil {
		obs.Logger(ctx).WarnContext(ctx, "chat.herdr_places", "socket", socket, "err", unmoved.Error())
	}
	listed, err := listHerdrWorkspaces(ctx, socket)
	if err != nil {
		return "", unmoved, err
	}
	workspace, err = listed.workspaceFor(ctx, socket, fleet.Of(request.Run.CWD))
	return workspace, unmoved, err
}
