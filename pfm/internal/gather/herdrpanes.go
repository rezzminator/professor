package gather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// decodeHerdrPanes reads a Herdr pane.list answer through its one parser
// (herdr.ParsePanes) and proves every pane's identity: a workspace-prefixed
// id, terminal and workspace, no id twice. A listing that fails any of these
// proves nothing about any pane in it, so it is an error, never an empty roster.
func decodeHerdrPanes(body json.RawMessage) ([]herdr.Pane, error) {
	panes, err := herdr.ParsePanes(body)
	if err != nil {
		return nil, err
	}
	return panes, provenHerdrPanes(panes)
}

func provenHerdrPanes(panes []herdr.Pane) error {
	seen := map[string]bool{}
	for index := range panes {
		pane := &panes[index]
		if pane.PaneID == "" || pane.TerminalID == "" || pane.WorkspaceID == "" ||
			!strings.HasPrefix(pane.PaneID, pane.WorkspaceID+":") || seen[pane.PaneID] {
			return errors.New("pane.list: malformed or duplicate pane identity")
		}
		seen[pane.PaneID] = true
	}
	return nil
}

// HerdrTerminalListed asks the Herdr server at socket whether a pane holding
// terminal is listed: one pane.list (herdr.ListPanes), its identities proved.
// A server that cannot answer, or answers malformed, is an error, never "not
// listed".
func HerdrTerminalListed(ctx context.Context, socket, terminal string) (bool, error) {
	panes, err := herdr.ListPanes(ctx, socket)
	if err != nil {
		return false, fmt.Errorf("list Herdr panes: %w", err)
	}
	if err := provenHerdrPanes(panes); err != nil {
		return false, err
	}
	for index := range panes {
		pane := &panes[index]
		if pane.TerminalID == terminal {
			return true, nil
		}
	}
	return false, nil
}

// ResolveHerdrSeat proves and persists the current address using the jail-aware
// process reader. Callers must refuse mutation on either an error or !owned.
func ResolveHerdrSeat(
	ctx context.Context,
	values paths.Values,
	seat fleetdb.Seat,
) (result fleetdb.Seat, owned bool, returnErr error) {
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	return proveHerdrSeatSnapshot(ctx, db, NewProcFS(values.ProcRoot), seat, nil, false)
}

// herdrSeatPane is the pane of panes seat lives in: the one holding its saved
// terminal (its saved pane when it has none); nil when none is listed.
func herdrSeatPane(seat fleetdb.Seat, panes []herdr.Pane) (*herdr.Pane, error) {
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
	return candidate, nil
}
