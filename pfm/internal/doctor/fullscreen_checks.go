package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printFullscreenDoctor checks every configured Claude account that asks for
// the fullscreen renderer (claudelaunch.WantsFullscreen) for the verdict
// Claude's boot canary leaves in its registry (installer.ClaudeRegistryFor),
// once per physical registry. A recorded auto-disable is a warning pfm install
// clears; a settings file or registry that cannot be read or parsed is its own
// failure, never folded into "ok": its real state is unknown. An account with
// no settings.json was never set up and says so on its own skipped line.
func printFullscreenDoctor(stdout io.Writer, home string, machine config.Config, tally *doctorTally) {
	seen := map[string]bool{}
	for _, account := range machine.Accounts {
		settings := filepath.Join(account.ConfigDir, "settings.json")
		if _, err := os.Stat(settings); errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(
				stdout,
				"doctor: fullscreen claude[%d] skipped: no settings.json at %s (account never set up)\n",
				account.ID,
				settings,
			)
			continue
		}
		wants, err := claudelaunch.WantsFullscreen(account.ConfigDir)
		if err != nil {
			tally.failures++
			fmt.Fprintf(stdout, "doctor: fullscreen claude[%d] could not read %s: %v\n", account.ID, settings, err)
			continue
		}
		if !wants {
			fmt.Fprintf(stdout, "doctor: fullscreen claude[%d] ok (tui not fullscreen)\n", account.ID)
			continue
		}
		registry := installer.ClaudeRegistryFor(home, account)
		if !firstVisit(seen, registry) {
			fmt.Fprintf(stdout, "doctor: fullscreen claude[%d] shares %s, reported above\n", account.ID, registry)
			continue
		}
		verdict, err := installer.ReadFullscreenAutoDisable(registry)
		if err != nil {
			tally.failures++
			fmt.Fprintf(stdout, "doctor: fullscreen claude[%d] could not read %s: %v\n", account.ID, registry, err)
			continue
		}
		if verdict == nil {
			fmt.Fprintf(stdout, "doctor: fullscreen claude[%d] ok\n", account.ID)
			continue
		}
		tally.warnings++
		fmt.Fprintf(
			stdout,
			"doctor: fullscreen claude[%d] fullscreen renderer auto-disabled by Claude's boot canary "+
				"(version %s, strikes %s) — run pfm install --yes\n",
			account.ID,
			verdict.Version,
			verdict.Strikes,
		)
	}
}
