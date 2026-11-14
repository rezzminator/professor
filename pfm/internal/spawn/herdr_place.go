package spawn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
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
// workspace's pane count; members, read by one pane.list the first time a
// placement asks what a workspace holds, each workspace's panes.
type herdrWorkspaces struct {
	byLabel map[string]string
	panes   map[string]int
	members map[string][]herdr.Pane
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

// herdrPlacement is where a chat belongs: where it sits (the zero value), in
// its own workspace once that wears its place's label (rename), or in its
// place's workspace (move into it; into "" when no workspace carries the label
// and one is created).
type herdrPlacement struct {
	rename string
	move   bool
	into   string
}

// placementOf is the one decision of where a chat of target, in pane self of
// workspace, belongs, for an open and the reconcile alike: a workspace
// labelled as its place holds it, whether it sits there or must go there;
// with none, its own workspace takes the place's label when it holds nothing
// but the place's chats and shells, and otherwise the place gets a workspace.
func (listed *herdrWorkspaces) placementOf(
	ctx context.Context,
	socket string,
	fleet place.Fleet,
	target place.Place,
	workspace, self string,
) (herdrPlacement, error) {
	if id := listed.byLabel[target.Label]; id != "" {
		return herdrPlacement{move: id != workspace, into: id}, nil
	}
	only, err := listed.holdsOnly(ctx, socket, fleet, target, workspace, self)
	if err != nil {
		return herdrPlacement{}, err
	}
	if only {
		return herdrPlacement{rename: workspace}, nil
	}
	return herdrPlacement{move: true}, nil
}

// holdsOnly says workspace holds nothing but target's: every pane is self, a
// live chat whose own directory files under target, or any other pane (a
// shell) working in a directory that does. A workspace no pane is listed in
// holds nothing of target's.
func (listed *herdrWorkspaces) holdsOnly(
	ctx context.Context,
	socket string,
	fleet place.Fleet,
	target place.Place,
	workspace, self string,
) (bool, error) {
	if listed.members == nil {
		panes, err := herdr.ListPanes(ctx, socket)
		if err != nil {
			return false, fmt.Errorf("list Herdr panes: %w", err)
		}
		listed.members = map[string][]herdr.Pane{}
		for index := range panes {
			listed.members[panes[index].WorkspaceID] = append(listed.members[panes[index].WorkspaceID], panes[index])
		}
	}
	held := listed.members[workspace]
	for index := range held {
		pane := &held[index]
		if pane.PaneID == self {
			continue
		}
		directory := pane.Directory()
		for position := range fleet.Rows {
			row := &fleet.Rows[position]
			if row.Backend == BackendHerdr && row.Kind.IsLiveSeat() && row.Socket == socket &&
				row.PaneID == pane.PaneID {
				// The pane's directory is its supervisor's; the chat names its own.
				directory = row.CWD
			}
		}
		if fleet.Of(directory).Root != target.Root {
			return false, nil
		}
	}
	return len(held) > 0, nil
}

// rename relabels workspace as label, a place's.
func (listed *herdrWorkspaces) rename(ctx context.Context, socket, workspace, label string) error {
	if err := herdr.RenameWorkspace(ctx, socket, workspace, label); err != nil {
		return err
	}
	// shortcut: another workspace sharing the old label is forgotten until the next listing.
	for old, id := range listed.byLabel {
		if id == workspace {
			delete(listed.byLabel, old)
		}
	}
	listed.byLabel[label] = workspace
	obs.Logger(ctx).InfoContext(ctx, "chat.herdr_workspace_renamed", "socket", socket, "workspace", workspace,
		"label", label)
	return nil
}

// HerdrOpensInPlace decides, by the decision the reconcile makes
// (placementOf), where an open of a chat that runs in cwd, asked from the
// caller's own Herdr pane (choice), happens: in that pane when its workspace
// is the chat's place's — renamed to the place's label first when no
// workspace carries it and the caller's holds nothing but the place's chats
// and shells — else in a new tab of the place's workspace (SpawnHerdr). It
// scans choice.Places once and leaves that scan in choice for the new tab a
// placement elsewhere births, and decides and renames under the socket's
// placement lock.
func HerdrOpensInPlace(
	ctx context.Context,
	values paths.Values,
	choice *BackendChoice,
	cwd string,
) (inPlace bool, returnErr error) {
	if choice.Places == nil {
		return false, errors.New("herdr open has no fleet places to file the chat under")
	}
	fleet, err := choice.Places(ctx)
	if err != nil {
		return false, fmt.Errorf("scan the fleet's places: %w", err)
	}
	choice.Places = func(context.Context) (*place.Fleet, error) { return fleet, nil }
	target := fleet.Of(cwd)
	if target.Root == "" {
		// A chat filed under no place has no workspace of its own to open in.
		return true, nil
	}
	socket := choice.HerdrSocket
	unlock, err := lockHerdrPlaces(ctx, values, socket)
	if err != nil {
		return false, err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	listed, err := listHerdrWorkspaces(ctx, socket)
	if err != nil {
		return false, err
	}
	placement, err := listed.placementOf(ctx, socket, *fleet, target, choice.HerdrWorkspace, choice.HerdrPane)
	if err != nil {
		return false, err
	}
	if placement.rename != "" {
		return true, listed.rename(ctx, socket, placement.rename, target.Label)
	}
	return !placement.move, nil
}

// ReconcileHerdr puts every live native chat on socket where placementOf says
// it belongs: its workspace renamed to its place's label, or the chat moved
// into its place's workspace as a new unfocused tab wearing its birth label,
// that workspace created when its place has none; only places with a live
// chat to hold get one. A move that empties a workspace closes it (Herdr's
// own rule). It places only a seat it proves under the seat's lock — never a
// shell or another program's pane, never the pane skip names (the one running
// this command). Every chat it could not place is in the joined error; the
// others still move.
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
		target := fleet.Of(row.CWD)
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
		if err := placeHerdrSeat(ctx, values, socket, skip, fleet, *row, target, &listed); err != nil {
			failures = append(
				failures,
				fmt.Errorf("chat %s stays out of workspace %q: %w", row.Name, target.Label, err),
			)
		}
	}
	return errors.Join(failures...)
}

// placeHerdrSeat puts row's proved live seat where placementOf says: its
// workspace renamed, or the seat moved into target's workspace and rewritten
// to the pane id Herdr gives it there, through the seat's CAS: a pane moved
// across workspaces takes a new id, and every reader finds the chat by its
// seat. A seat that ended since the scan, already sits right, or proves to be
// the pane skip names stays. The workspace is created only once the seat
// proved movable, so a place gets one only for a chat it holds.
func placeHerdrSeat(
	ctx context.Context,
	values paths.Values,
	socket, skip string,
	fleet place.Fleet,
	row compose.Row,
	target place.Place,
	listed *herdrWorkspaces,
) (returnErr error) {
	seatID := row.SeatID
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
	placement, err := listed.placementOf(ctx, socket, fleet, target, proved.HerdrWorkspace, proved.HerdrPane)
	if err != nil {
		return err
	}
	if placement.rename != "" {
		return listed.rename(ctx, socket, placement.rename, target.Label)
	}
	if !placement.move {
		return nil
	}
	if proved.HerdrTerminalID == "" || proved.SeatHostPID <= 0 {
		return fmt.Errorf("seat %s has no proved terminal and supervisor", seatID)
	}
	workspace := placement.into
	if workspace == "" {
		if workspace, err = listed.workspaceFor(ctx, socket, target); err != nil {
			return err
		}
	}
	// The new tab is named as a born tab is: Herdr labels it with the move.
	body, err := herdr.SeatRequest(ctx, proved.HerdrSocket, "pane.move", map[string]any{
		"pane_id": proved.HerdrPane,
		"destination": map[string]any{
			"type": "new_tab", "workspace_id": workspace, "label": BirthTabLabel(row.Name, row.CWD),
		},
		herdrFocusKey: false,
	})
	if err != nil {
		return fmt.Errorf("herdr pane.move %s: %w", proved.HerdrPane, err)
	}
	var reply struct {
		Type string `json:"type"`
		Move struct {
			Previous string `json:"previous_pane_id"`
			// Closed is the workspace the move emptied, which Herdr closed.
			Closed string `json:"closed_workspace_id"`
			Pane   struct {
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
	listed.moved(proved.HerdrWorkspace, proved.HerdrPane, reply.Move.Closed, herdr.Pane{
		PaneID: moved.ID, WorkspaceID: workspace, TerminalID: moved.Terminal, CWD: row.CWD,
	})
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

// moved records pane, moved out of pane from in workspace left, as Herdr
// answered: left loses it, and is gone when Herdr closed it (closed).
func (listed *herdrWorkspaces) moved(left, from, closed string, pane herdr.Pane) {
	listed.panes[left]--
	listed.panes[pane.WorkspaceID]++
	if listed.members != nil {
		listed.members[left] = slices.DeleteFunc(listed.members[left], func(held herdr.Pane) bool {
			return held.PaneID == from
		})
		listed.members[pane.WorkspaceID] = append(listed.members[pane.WorkspaceID], pane)
	}
	if closed == "" {
		return
	}
	delete(listed.panes, closed)
	delete(listed.members, closed)
	for label, id := range listed.byLabel {
		if id == closed {
			delete(listed.byLabel, label)
		}
	}
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
