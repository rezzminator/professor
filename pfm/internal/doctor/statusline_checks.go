package doctor

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printStatusLineOverlayDoctor checks every configured Claude account's
// settings.json for a statusLine.command that still names the raw `pfm
// statusline` instead of the pfm-statusline overlay printHostOverlayDoctor's
// own symlink checks cover — the wiring gap issue #14 F1 named, invisible
// from the prompt itself. A settings file that cannot be read or parsed is
// its own failure, distinct from and never folded into "not configured": an
// unreadable file's actual wiring is unknown, not absent — installer.
// ReadStatusLineCommand's two return values keep the two states apart.
func printStatusLineOverlayDoctor(stdout io.Writer, home string, machine config.Config) (failures int) {
	overlayCommand := installer.StatusLineOverlayCommand(home)
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
		command, readErr := installer.ReadStatusLineCommand(path)
		if readErr != nil {
			failures++
			fmt.Fprintf(
				stdout,
				"doctor: host_overlay statusline claude[%d] could not read %s: %v\n",
				account.ID,
				path,
				readErr,
			)
			continue
		}
		if command == "" || command == overlayCommand || !installer.RawStatusLineCommand(home, command) {
			continue
		}
		failures++
		fmt.Fprintf(
			stdout,
			"doctor: host_overlay statusline claude[%d] command=%q, want the overlay — run pfm install --yes\n",
			account.ID,
			command,
		)
	}
	return failures
}
