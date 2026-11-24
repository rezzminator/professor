package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// loadRetiredLists reads the clone's retired lists for every doctor check
// that needs one; a list that cannot load is one FAIL row, and those checks
// then run on empty lists.
func loadRetiredLists(stdout io.Writer, home string) (retired.Lists, int) {
	lists, err := retired.LoadLists(home)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: retired_lists FAIL %v — restore %s in the clone\n", err, retired.FileRel)
		return retired.Lists{}, 1
	}
	return lists, 0
}

// printRetiredClaudePluginsDoctor warns once per plugin the retired lists
// retired with no successor (ids) that still leaves a trace in the Claude store
// of home or one of dirs (installer.RetiredClaudePluginGaps), naming each and
// pfm install as the repair, or one build still plans, which install keeps; a
// record it cannot read is one failure. An unread marketplace list judges by
// claude.plugins alone, and each gap's repair names that list first: install
// removes no retired plugin while it is unread; the claude_plugins check fails it.
func printRetiredClaudePluginsDoctor(
	stdout io.Writer, home string, dirs, ids []string, build installer.ClaudePluginBuild, tally *doctorTally,
) {
	build, marketErr := build.WithMarketplacePlugins()
	gaps, err := installer.RetiredClaudePluginGaps(home, dirs, ids, build)
	if err != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins could not look for retired plugins: %v\n", err)
	}
	if marketErr != nil {
		for index := range gaps {
			gaps[index].Fix = "pfm install --yes removes no retired plugin while the marketplace plugin list is unread (" +
				marketErr.Error() + "); repair that first, then " + installer.ClaudePluginRepair
		}
	}
	printClaudePluginGaps(stdout, gaps, tally, func(installer.ClaudePluginGap) bool { return false })
}
