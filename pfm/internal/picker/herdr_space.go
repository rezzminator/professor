package picker

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// herdrSpace is the label of the Herdr space whose pane runs this picker, when
// the picker opened in home: Herdr starts a new tab there whatever its space,
// so the space's label is the one sign of the project the tab was opened for.
// "" outside Herdr or in any other directory; a failed lookup is reported and
// the picker opens as it does in home.
func herdrSpace(ctx context.Context, home string, stderr io.Writer) string {
	caller := resolve.CallerEnvironmentFrom(paths.OSEnv{})
	if caller.HerdrSocket == "" || caller.HerdrPane == "" {
		return ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "pfm ls: herdr space: read the working directory: %v\n", err)
		return ""
	}
	if home == "" || place.Clean(cwd) != place.Clean(home) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, herdr.ReportDeadline)
	defer cancel()
	panes, err := herdr.ListPanes(ctx, caller.HerdrSocket)
	if err != nil {
		fmt.Fprintf(stderr, "pfm ls: herdr space of pane %s: %v\n", caller.HerdrPane, err)
		return ""
	}
	// The live listing, not HERDR_WORKSPACE_ID: a tab moved to another space keeps its birth env.
	workspace := ""
	for index := range panes {
		if panes[index].PaneID == caller.HerdrPane {
			workspace = panes[index].WorkspaceID
		}
	}
	if workspace == "" {
		fmt.Fprintf(
			stderr,
			"pfm ls: herdr space: pane %s is not in %s's pane list\n",
			caller.HerdrPane,
			caller.HerdrSocket,
		)
		return ""
	}
	body, err := herdr.SeatRequest(ctx, caller.HerdrSocket, "workspace.list", struct{}{})
	if err == nil {
		var labels map[string]string
		if labels, err = herdr.ParseWorkspaceList(body); err == nil {
			return labels[workspace]
		}
	}
	fmt.Fprintf(stderr, "pfm ls: herdr space %s: %v\n", workspace, err)
	return ""
}
