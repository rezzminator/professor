package doctor

import (
	"fmt"
	"io"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printClaudePluginSourceDoctor prints the untallied claude_plugins source
// row: where Professor's own plugins install from, claude.pluginSource
// (pfmconfig.ClaudePrefs.PluginsFromCheckout), and whether the machine config
// file or the default set it. Every plugin row after it judges against that
// source, never against the build's version.
func printClaudePluginSourceDoctor(stdout io.Writer, config pfmconfig.Config) {
	source, from := pfmconfig.PluginSourceRelease, "github.com/"+installer.ProfessorMarketplaceRepository
	if config.Claude.PluginsFromCheckout() {
		source, from = pfmconfig.PluginSourceCheckout, "their checkouts"
	}
	fmt.Fprintf(stdout, "doctor: claude_plugins source: %s=%s (%s) — Professor's plugins install from %s\n",
		pfmconfig.KeyClaudePluginSource, source, config.Source(pfmconfig.KeyClaudePluginSource), from)
}
