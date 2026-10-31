package gather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/herdr"
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
