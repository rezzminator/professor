package doctor

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printClaudePluginsDoctor checks every configured Claude account for the
// plugins pfm install ensures: its settings.json for the enabled
// plugins (installer.ClaudePluginGaps), once per physical file,
// since accounts may share one through a symlink; and its own config dir's
// install record (installer.ClaudePluginsNotInstalled), once per physical
// dir. Each gap is a warning — the plugins are optional extras pfm runs
// without — and a complete account gets one ok line. A file that cannot be
// read or parsed is its own failure, never folded into "missing": its real
// state is unknown. An account with no settings.json was never set up — a
// fresh home stays clean — and says so on its own skipped line.
func printClaudePluginsDoctor(stdout io.Writer, machine config.Config, tally *doctorTally) {
	seenSettingsFiles := map[string]bool{}
	seenConfigDirs := map[string]bool{}
	for _, account := range machine.Accounts {
		path := filepath.Join(account.ConfigDir, "settings.json")
		gaps, readErr := installer.ClaudePluginGaps(path)
		if errors.Is(readErr, installer.ErrClaudeSettingsAbsent) {
			fmt.Fprintf(
				stdout,
				"doctor: claude_plugins claude[%d] skipped: %v (account never set up)\n",
				account.ID,
				readErr,
			)
			continue
		}
		if firstVisit(seenSettingsFiles, path) {
			if readErr != nil {
				tally.failures++
				fmt.Fprintf(
					stdout,
					"doctor: claude_plugins claude[%d] could not read %s: %v\n",
					account.ID,
					path,
					readErr,
				)
				continue
			}
		} else {
			gaps = nil
		}
		if firstVisit(seenConfigDirs, account.ConfigDir) {
			missing, err := installer.ClaudePluginsNotInstalled(account.ConfigDir)
			if err != nil {
				tally.failures++
				fmt.Fprintf(
					stdout,
					"doctor: claude_plugins claude[%d] could not read its install record: %v\n",
					account.ID,
					err,
				)
				continue
			}
			for _, id := range missing {
				gaps = append(gaps, "plugin "+id+" not installed in "+account.ConfigDir)
			}
		}
		if len(gaps) == 0 {
			fmt.Fprintf(stdout, "doctor: claude_plugins claude[%d] ok\n", account.ID)
			continue
		}
		for _, gap := range gaps {
			tally.warnings++
			fmt.Fprintf(stdout, "doctor: claude_plugins claude[%d] %s — run pfm install --yes\n", account.ID, gap)
		}
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
