package themecmd

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
	"github.com/rezzminator/professor/pfm/internal/themetmux"
)

// ConvergeTmux is name-sync's theme door: it puts the current theme on every
// live chat server, so a server born before `pfm theme on` (or one whose
// options a restart reset) joins the sync. It prints nothing and touches
// nothing while the sync is off. A server that refuses is named on stderr and
// counted in the return, which name-sync folds into its unverified count and
// exit code.
func ConvergeTmux(
	ctx context.Context,
	values paths.Values,
	sockets []string,
	stdout, stderr io.Writer,
) (unverified int) {
	return applyThemeToSockets(
		ctx, themetmux.AtDir(values.TmuxDir), themesync.DefaultStateDir(values.Home), sockets,
		themesync.SyncOn, themesync.ApplyTmuxServer, stdout, stderr,
	)
}

func applyThemeToSockets(
	ctx context.Context,
	servers themesync.TmuxServers,
	stateDir string,
	sockets []string,
	syncOn func(string) bool,
	apply func(context.Context, themesync.TmuxServers, string, string) error,
	stdout, stderr io.Writer,
) (unverified int) {
	if !syncOn(stateDir) {
		return 0
	}
	applied := 0
	for _, socket := range sockets {
		if err := apply(ctx, servers, stateDir, socket); err != nil {
			fmt.Fprintf(stderr, "pfm name-sync: tmux theme %s: %v\n", socket, err)
			unverified++
			continue
		}
		applied++
	}
	fmt.Fprintf(stdout, "tmux theme applied: %d\n", applied)
	return unverified
}
