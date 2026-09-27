package doctor

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printClaudePluginsDoctor checks every configured Claude account's
// settings.json for the plugins and env pfm install ensures
// (installer.ClaudePluginGaps): one warning line per gap — the plugins are
// optional extras pfm runs without — and one ok line per complete account. A settings file that cannot be read or parsed is its own
// failure, never folded into "missing": its real state is unknown. An account
// with no settings.json at all was never set up — a fresh home stays clean —
// and says so on its own skipped line.
func printClaudePluginsDoctor(stdout io.Writer, machine config.Config, tally *doctorTally) {
	seenSettingsFiles := map[string]bool{}
	for _, account := range machine.Accounts {
		path := filepath.Join(account.ConfigDir, "settings.json")
		physical, err := filepath.EvalSymlinks(path)
		if err != nil {
			physical = path
		}
		physical = filepath.Clean(physical)
		if seenSettingsFiles[physical] {
			continue
		}
		seenSettingsFiles[physical] = true
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
		if readErr != nil {
			tally.failures++
			fmt.Fprintf(stdout, "doctor: claude_plugins claude[%d] could not read %s: %v\n", account.ID, path, readErr)
			continue
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
