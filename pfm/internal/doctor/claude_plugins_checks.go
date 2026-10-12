package doctor

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printClaudePluginsDoctor checks the shared store once for the plugins build
// ensures: the -dev ids and their copies on an -alpha build with checkouts, the
// GitHub ids otherwise, each fallback named on its own informational line. A
// listed id pfm cannot install is one line naming it, its reason and its repair,
// in place of the "run pfm install --yes" lines that would not close it, even
// on a store never set up; with the plugin records unreadable, each id that
// needs a known marketplace and is not proven enabled and installed is named as
// one pfm cannot check. A bare name of ours that is not enabled or not installed
// is told the repository it installs from and that a failing install means the
// entry is wrong. A plugin enabled under two or more keys, and a
// marketplace pfm install adds that is registered from another source, are one
// warning each with its repair. An unreadable marketplace record is a failure
// for the ids that need it; every other listed plugin is still checked.
func printClaudePluginsDoctor(stdout io.Writer, store string, build installer.ClaudePluginBuild, tally *doctorTally) {
	targets := installer.ClaudePluginTargets(build)
	for index := range targets {
		target := &targets[index]
		if target.Fallback != "" {
			fmt.Fprintln(stdout, "doctor: claude_plugins "+target.FallbackNote())
		}
	}
	unresolved, recordErr := installer.ClaudePluginsUnresolved(store, targets)
	if recordErr != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read the plugin records in %s: %v\n", store, recordErr)
	}
	var cannotInstall []installer.ClaudePluginGap
	resolved := make([]installer.ClaudePluginTarget, 0, len(targets))
	for index := range targets {
		target := &targets[index]
		if gap, ok := unresolved[target.ID]; ok {
			cannotInstall = append(cannotInstall, installer.ClaudePluginGap{
				Problem: "plugin " + target.ID + " cannot be installed: " + gap.Problem, Fix: gap.Fix,
			})
			continue
		}
		if recordErr != nil && target.NeedsKnownMarketplace() {
			// The unreadable records hide whether its marketplace is known; the id itself comes from config.
			if !enabledAndInstalled(store, *target) {
				cannotInstall = append(cannotInstall, installer.ClaudePluginGap{
					Problem: "plugin " + target.ID + " cannot be checked: marketplace " + target.MarketplaceName +
						" cannot be looked up while the plugin records are unreadable, and its " +
						pfmconfig.KeyClaudePlugins + " entry has no source",
					Fix: unreadableRecordsFix,
				})
			}
			continue
		}
		resolved = append(resolved, *target)
	}
	targets = resolved
	path := filepath.Join(store, "settings.json")
	gaps, err := installer.ClaudePluginGaps(path, targets)
	if errors.Is(err, installer.ErrClaudeSettingsAbsent) {
		fmt.Fprintf(stdout, "doctor: claude_plugins skipped: %v (store never set up)\n", err)
		printClaudePluginGaps(stdout, cannotInstall, tally)
		return
	}
	if err != nil {
		tally.fail()
		printClaudePluginGaps(stdout, cannotInstall, tally)
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read %s: %v\n", path, err)
		return
	}
	missing, err := installer.ClaudePluginsNotInstalled(store, targets)
	if err != nil {
		tally.fail()
		printClaudePluginGaps(stdout, cannotInstall, tally)
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
	gaps = bareNameInstallFixes(gaps, targets)
	marketplaceGaps, marketplaceErr := installer.ClaudePluginMarketplaceGaps(store, targets)
	// An unreadable known_marketplaces.json recordErr already named is one failure, reported once.
	knownMarketplaces := filepath.Join(store, "plugins", "known_marketplaces.json")
	if marketplaceErr != nil && (recordErr == nil || !strings.Contains(recordErr.Error(), knownMarketplaces)) {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read %s: %v\n", knownMarketplaces, marketplaceErr)
	}
	gaps = append(gaps, marketplaceGaps...)
	gaps = append(gaps, cannotInstall...)
	if len(gaps) == 0 && recordErr == nil && marketplaceErr == nil {
		fmt.Fprintln(stdout, "doctor: claude_plugins ok")
		return
	}
	printClaudePluginGaps(stdout, gaps, tally)
}

// unreadableRecordsFix is the repair of an id doctor could not check for want of a readable record.
const unreadableRecordsFix = "repair the unreadable file named above, then run pfm doctor again"

// enabledAndInstalled reports whether target is proven enabled in the store's settings.json and
// installed in its plugins/installed_plugins.json, which needs no marketplace lookup. A record
// that cannot be read proves nothing.
func enabledAndInstalled(store string, target installer.ClaudePluginTarget) bool {
	one := []installer.ClaudePluginTarget{target}
	gaps, err := installer.ClaudePluginGaps(filepath.Join(store, "settings.json"), one)
	if err != nil || len(gaps) != 0 {
		return false
	}
	missing, err := installer.ClaudePluginsNotInstalled(store, one)
	return err == nil && len(missing) == 0
}

// bareNameInstallFix is the repair for a bare plugin name of ours that is not enabled or not
// installed. pfm cannot know offline whether the repository exists, so after the generic repair
// the line names the source pfm install uses and says what a failing install means: a typo
// installs from nothing, and "run pfm install --yes" alone would never close it.
func bareNameInstallFix(target installer.ClaudePluginTarget) string {
	return installer.ClaudePluginRepair + "; if it keeps failing, " + installer.BareClaudePluginRepair(target.Name)
}

// bareNameInstallFixes swaps the generic repair of every "not enabled" and "not installed" gap of
// a bare name that installs from GitHub for bareNameInstallFix. A local -alpha checkout copy has a
// checkout to name instead and keeps its repair.
func bareNameInstallFixes(
	gaps []installer.ClaudePluginGap,
	targets []installer.ClaudePluginTarget,
) []installer.ClaudePluginGap {
	for index := range gaps {
		if gaps[index].Fix != installer.ClaudePluginRepair {
			continue
		}
		for position := range targets {
			target := &targets[position]
			if target.Own && !target.Local && strings.HasPrefix(gaps[index].Problem, "plugin "+target.ID+" not ") {
				gaps[index].Fix = bareNameInstallFix(*target)
				break
			}
		}
	}
	return gaps
}

// printClaudePluginGaps prints one warning per gap, its problem and its repair.
func printClaudePluginGaps(stdout io.Writer, gaps []installer.ClaudePluginGap, tally *doctorTally) {
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
