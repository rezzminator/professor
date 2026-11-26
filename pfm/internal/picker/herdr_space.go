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
	label, err := herdr.PaneWorkspaceLabel(ctx, caller.HerdrSocket, caller.HerdrPane)
	if err != nil {
		fmt.Fprintf(stderr, "pfm ls: herdr space: %v\n", err)
		return ""
	}
	return label
}
