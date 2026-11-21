package spawn

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/place"
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
// place, else one created for it, unfocused, opening in the place's directory
// (a space's, the folder of the chat that asked). It never renames, closes or
// duplicates a workspace. A workspace it created comes with the tab Herdr opens
// every new workspace with, a shell (starting): the chat placed there takes its
// place, so the place holds nothing but its chats.
func (listed herdrWorkspaces) workspaceFor(ctx context.Context, socket string, target place.Place) (
	workspace, starting string, err error,
) {
	if target.Root == "" || target.Label == "" {
		return "", "", fmt.Errorf("herdr workspace: place %+v has no root or label", target)
	}
	if id := listed.byLabel[target.Label]; id != "" {
		return id, "", nil
	}
	params := map[string]any{"label": target.Label, herdrFocusKey: false}
	if directory := target.Directory(); directory != "" {
		params["cwd"] = directory // a space with no known folder lets Herdr choose
	}
	body, err := herdr.SeatRequest(ctx, socket, "workspace.create", params)
	if err != nil {
		return "", "", fmt.Errorf("create Herdr workspace %q at %s: %w", target.Label, target.Directory(), err)
	}
	var reply struct {
		Type      string `json:"type"`
		Workspace struct {
			ID    string `json:"workspace_id"`
			Label string `json:"label"`
		} `json:"workspace"`
		Tab struct {
			ID string `json:"tab_id"`
		} `json:"tab"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return "", "", fmt.Errorf("herdr workspace %q may exist: decode reply: %w", target.Label, err)
	}
	if reply.Type != "workspace_created" || reply.Workspace.ID == "" || reply.Workspace.Label != target.Label ||
		reply.Tab.ID == "" {
		return "", "", fmt.Errorf("herdr workspace %q may exist: unexpected reply %s", target.Label, body)
	}
	listed.byLabel[target.Label] = reply.Workspace.ID
	listed.panes[reply.Workspace.ID] = 1
	return reply.Workspace.ID, reply.Tab.ID, nil
}

// closeStarting closes starting, the shell tab Herdr created workspace with,
// once a chat moved in beside it. The chat is placed either way: a tab that
// stays open is logged, never a failed placement.
func (listed *herdrWorkspaces) closeStarting(ctx context.Context, socket, workspace, starting string) {
	if _, err := herdr.SeatRequest(ctx, socket, "tab.close", map[string]string{"tab_id": starting}); err != nil {
		obs.Logger(ctx).WarnContext(ctx, "chat.herdr_starting_tab", "socket", socket, "workspace", workspace,
			"tab", starting, "err", err.Error())
		return
	}
	listed.panes[workspace]--
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
