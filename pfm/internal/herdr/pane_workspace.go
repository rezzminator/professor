package herdr

import (
	"context"
	"fmt"
)

// PaneWorkspaceLabel is the label of the workspace pane sits in now, read from
// the live pane and workspace lists, never HERDR_WORKSPACE_ID: a tab moved to
// another workspace keeps its birth environment. A pane the server does not
// list is an error, never an empty label.
func PaneWorkspaceLabel(ctx context.Context, socket, pane string) (string, error) {
	panes, err := ListPanes(ctx, socket)
	if err != nil {
		return "", fmt.Errorf("herdr workspace of pane %s: %w", pane, err)
	}
	workspace := ""
	for index := range panes {
		if panes[index].PaneID == pane {
			workspace = panes[index].WorkspaceID
		}
	}
	if workspace == "" {
		return "", fmt.Errorf("herdr workspace of pane %s: the pane is not in %s's pane list", pane, socket)
	}
	body, err := SeatRequest(ctx, socket, "workspace.list", struct{}{})
	if err != nil {
		return "", fmt.Errorf("herdr workspace %s: %w", workspace, err)
	}
	labels, err := ParseWorkspaceList(body)
	if err != nil {
		return "", fmt.Errorf("herdr workspace %s: %w", workspace, err)
	}
	return labels[workspace], nil
}
