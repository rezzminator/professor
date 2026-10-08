package doctor

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printClaudePluginsDoctor checks the shared store once for the plugins build
// ensures: the -dev ids and their copies on an -alpha build with checkouts, the
// GitHub ids otherwise, each fallback named on its own informational line.
func printClaudePluginsDoctor(stdout io.Writer, store string, build installer.ClaudePluginBuild, tally *doctorTally) {
	targets := installer.ClaudePluginTargets(build)
	for index := range targets {
		target := &targets[index]
		if target.Fallback != "" {
			fmt.Fprintln(stdout, "doctor: claude_plugins "+target.FallbackNote())
		}
	}
	path := filepath.Join(store, "settings.json")
	gaps, err := installer.ClaudePluginGaps(path, targets)
	if errors.Is(err, installer.ErrClaudeSettingsAbsent) {
		fmt.Fprintf(stdout, "doctor: claude_plugins skipped: %v (store never set up)\n", err)
		return
	}
	if err != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read %s: %v\n", path, err)
		return
	}
	missing, err := installer.ClaudePluginsNotInstalled(store, targets)
	if err != nil {
		tally.fail()
		fmt.Fprintf(
			stdout,
			"doctor: claude_plugins could not read %s: %v\n",
			filepath.Join(store, "plugins", "installed_plugins.json"),
			err,
		)
		return
	}
	for _, id := range missing {
		gaps = append(gaps, installer.ClaudePluginGap{
			Problem: "plugin " + id + " not installed in " + store, Fix: installer.ClaudePluginRepair,
		})
	}
	marketplaceGaps, err := installer.ClaudePluginMarketplaceGaps(store, targets)
	if err != nil {
		tally.fail()
		fmt.Fprintf(
			stdout,
			"doctor: claude_plugins could not read %s: %v\n",
			filepath.Join(store, "plugins", "known_marketplaces.json"),
			err,
		)
		return
	}
	gaps = append(gaps, marketplaceGaps...)
	if len(gaps) == 0 {
		fmt.Fprintln(stdout, "doctor: claude_plugins ok")
		return
	}
	for _, gap := range gaps {
		tally.warn()
		fmt.Fprintf(stdout, "doctor: claude_plugins %s — %s\n", gap.Problem, gap.Fix)
	}
}

// firstVisit reports whether path's physical location is new to seen, and
// marks it seen.
func firstVisit(seen map[string]bool, path string) bool {
	physical := paths.PhysicalPath(path)
	if seen[physical] {
		return false
	}
	seen[physical] = true
	return true
}
