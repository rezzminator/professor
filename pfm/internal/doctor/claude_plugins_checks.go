package doctor

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printClaudePluginsDoctor checks the shared store once.
func printClaudePluginsDoctor(stdout io.Writer, store string, tally *doctorTally) {
	path := filepath.Join(store, "settings.json")
	gaps, err := installer.ClaudePluginGaps(path)
	if errors.Is(err, installer.ErrClaudeSettingsAbsent) {
		fmt.Fprintf(stdout, "doctor: claude_plugins skipped: %v (store never set up)\n", err)
		return
	}
	if err != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read %s: %v\n", path, err)
		return
	}
	for i := range gaps {
		gaps[i] += " in " + path
	}
	missing, err := installer.ClaudePluginsNotInstalled(store)
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
		gaps = append(gaps, "plugin "+id+" not installed in "+store)
	}
	if len(gaps) == 0 {
		fmt.Fprintln(stdout, "doctor: claude_plugins ok")
		return
	}
	for _, gap := range gaps {
		tally.warn()
		fmt.Fprintf(stdout, "doctor: claude_plugins %s — run pfm install --yes\n", gap)
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
