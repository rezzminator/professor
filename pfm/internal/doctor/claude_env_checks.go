package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// printClaudeEnvDoctor checks claude.env against the settings file install
// writes it into (settingsPath) and every file the ownership ledger under
// managedRoot names (installer.ClaudeEnvGaps): each gap is a failure, and an
// unreadable ledger or settings file is a failure, never ok. A key no plugin
// of build names by its prefix is a warning.
func printClaudeEnvDoctor(
	stdout io.Writer,
	managedRoot, settingsPath string,
	env map[string]string,
	build installer.ClaudePluginBuild,
	tally *doctorTally,
) {
	if len(installer.ClaudeEnvUnknownKeys(env, nil)) > 0 {
		if build, err := build.WithMarketplacePlugins(); err != nil {
			tally.warn()
			fmt.Fprintf(
				stdout,
				"doctor: claude_env the plugin list is unread, so no key's prefix was checked: %v\n",
				err,
			)
		} else {
			for _, key := range installer.ClaudeEnvUnknownKeys(env, installer.ClaudePluginTargets(build)) {
				tally.warn()
				fmt.Fprintf(stdout,
					"doctor: claude_env %s: no plugin pfm installs is named by its prefix — "+
						"name it {PLUGIN}_{OPTION} after a plugin pfm installs\n", key)
			}
		}
	}
	settingsPath, err := installer.ClaudeEnvSettingsPath(managedRoot, settingsPath)
	if err != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_env FAIL %v — repair it, then run pfm install --yes\n", err)
		return
	}
	gaps, err := installer.ClaudeEnvGaps(managedRoot, settingsPath, env)
	if err != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_env FAIL %v — repair it, then run pfm install --yes\n", err)
		return
	}
	reported, pluginLogsMissing := 0, false
	for _, gap := range gaps {
		if gap.Key == installer.PluginLogDirEnv && gap.Missing && obs.AlphaBuild(build.Version) {
			pluginLogsMissing = true
			continue
		}
		reported++
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_env FAIL %s — %s\n", gap.Problem, gap.Fix)
	}
	switch {
	case reported > 0:
	case pluginLogsMissing:
		fmt.Fprintln(stdout, "doctor: claude_env ok — PLUGIN_LOG_DIR readiness is checked by plugin_logs")
	case len(env) == 0:
		fmt.Fprintln(stdout, "doctor: claude_env ok — claude.env is empty")
	default:
		fmt.Fprintf(stdout, "doctor: claude_env ok — %d keys in %s\n", len(env), settingsPath)
	}
}
