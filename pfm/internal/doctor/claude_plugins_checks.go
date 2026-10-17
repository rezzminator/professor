package doctor

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// doctorClaudePluginBuild is the build doctor checks the plugins of: this
// pfm's version, home, claude.pluginCheckoutRoot, claude.plugins and the
// source clone a prior install anchored on (installer.GlobalSourceRepo, the
// marker else the default path), whose plugins/ an -alpha build copies from
// when the key is unset. A clone that cannot be resolved rides as
// SourceRepoErr, named in the fallback line. pluginsDefault says claude.plugins
// is unset: an -alpha build then checks every plugin the clone's marketplace
// lists.
func doctorClaudePluginBuild(
	version, home string,
	claude pfmconfig.Claude,
	pluginsDefault bool,
) installer.ClaudePluginBuild {
	sourceRepo, sourceRepoErr := installer.GlobalSourceRepo(home)
	return installer.ClaudePluginBuild{
		Version:        version,
		Home:           home,
		CheckoutRoot:   claude.PluginCheckoutRoot,
		SourceRepo:     sourceRepo,
		SourceRepoErr:  sourceRepoErr,
		Plugins:        claude.Plugins,
		PluginsDefault: pluginsDefault,
	}
}

// engineSyncHookOmission is the line printed when a set claude.plugins leaves out engine-sync: the
// plugin's hooks are the only door that rebuilds the Codex and OpenCode mirrors.
const engineSyncHookOmission = "doctor: claude.plugins omits engine-sync — no hook rebuilds the Codex and OpenCode " +
	"mirrors; add engine-sync to claude.plugins\n"

// printEngineSyncHookDoctor warns once when claude.plugins is set (pluginsDefault false) and no entry
// is the engine-sync plugin, bare or as engine-sync@marketplace. An unset list is the default, which
// lists it, so nothing prints.
func printEngineSyncHookDoctor(
	stdout io.Writer,
	plugins []pfmconfig.ClaudePlugin,
	pluginsDefault bool,
	tally *doctorTally,
) {
	if pluginsDefault {
		return
	}
	if slices.ContainsFunc(plugins, func(plugin pfmconfig.ClaudePlugin) bool {
		return plugin.Name() == "engine-sync"
	}) {
		return
	}
	tally.warn()
	fmt.Fprint(stdout, engineSyncHookOmission)
}

// printClaudePluginsDoctor checks the shared store once for the plugins build
// ensures: the -dev ids and their copies on an -alpha build with checkouts, the
// GitHub ids otherwise, each -alpha fallback a gap. An -alpha build also checks
// every plugin the source clone's marketplace lists that claude.plugins does
// not, installed with no copy enabled; a marketplace list it cannot read is a
// failure. On an -alpha build each gap of a plugin of ours claude.plugins
// lists is a failure, every other gap a warning; a
// bare name whose source clone or checkout cannot be read is one warning
// naming the error, neither copy checked. A
// listed id pfm cannot install is one line naming it, its reason and its repair,
// in place of the "run pfm install --yes" lines that would not close it, even
// on a store never set up; with the plugin records unreadable, each id that
// needs a known marketplace and is not proven enabled and installed is named as
// one pfm cannot check. A bare name of ours that is not enabled or not installed
// is told the repository it installs from and that a failing install means the
// entry is wrong. A plugin enabled under two or more keys, and a
// marketplace pfm install adds that is registered from another source, are one
// warning each with its repair. An unreadable marketplace record is a failure
// for the ids that need it; every other listed plugin is still checked. An
// enabled callmeter -dev copy whose binary under env's callmeter home is gone
// is a gap (installer.CallmeterBinaryGap).
func printClaudePluginsDoctor(
	stdout io.Writer,
	store string,
	env paths.Env,
	build installer.ClaudePluginBuild,
	tally *doctorTally,
) {
	build, listErr := build.WithMarketplacePlugins()
	if listErr != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins %v — repair it, then run pfm install --yes\n", listErr)
	}
	targets := installer.ClaudePluginTargets(build)
	failing := listedPluginGaps(build.Version, targets)
	var cannotInstall []installer.ClaudePluginGap
	for index := range targets {
		if targets[index].Fallback != "" {
			cannotInstall = append(cannotInstall, installer.ClaudePluginFallbackGap(targets[index]))
		}
	}
	unresolved, recordErr := installer.ClaudePluginsUnresolved(store, targets)
	if recordErr != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read the plugin records in %s: %v\n", store, recordErr)
	}
	resolved := make([]installer.ClaudePluginTarget, 0, len(targets))
	for index := range targets {
		target := &targets[index]
		if target.Undecided != "" {
			// The build cannot tell which copy it ensures, so neither is checked.
			cannotInstall = append(cannotInstall, installer.ClaudePluginGap{
				Problem: "plugin " + target.Name + " cannot be checked: " + target.UndecidedNote(),
				Fix:     undecidedCopyFix,
			})
			continue
		}
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
		printClaudePluginGaps(stdout, cannotInstall, tally, failing)
		return
	}
	if err != nil {
		tally.fail()
		printClaudePluginGaps(stdout, cannotInstall, tally, failing)
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read %s: %v\n", path, err)
		return
	}
	missing, err := installer.ClaudePluginsNotInstalled(store, targets)
	if err != nil {
		tally.fail()
		printClaudePluginGaps(stdout, cannotInstall, tally, failing)
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
	// Claude runs an installed -dev copy from its snapshot, which a refreshed copy does not move;
	// a target not installed is named above, its install the one repair.
	installedTargets := slices.DeleteFunc(slices.Clone(targets), func(target installer.ClaudePluginTarget) bool {
		return slices.Contains(missing, target.ID)
	})
	cacheGaps, cacheErr := installer.ClaudePluginCacheGaps(store, installedTargets)
	if cacheErr != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read the plugin cache: %v\n", cacheErr)
	}
	gaps = append(gaps, cacheGaps...)
	gaps = bareNameInstallFixes(gaps, targets)
	marketplaceGaps, marketplaceErr := installer.ClaudePluginMarketplaceGaps(store, targets)
	// An unreadable known_marketplaces.json recordErr already named is one failure, reported once.
	knownMarketplaces := filepath.Join(store, "plugins", "known_marketplaces.json")
	if marketplaceErr != nil && (recordErr == nil || !strings.Contains(recordErr.Error(), knownMarketplaces)) {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: claude_plugins could not read %s: %v\n", knownMarketplaces, marketplaceErr)
	}
	gaps = append(gaps, marketplaceGaps...)
	for index := range targets {
		if gap, found := installer.CallmeterBinaryGap(env, build.Home, targets[index]); found {
			gaps = append(gaps, gap)
		}
	}
	gaps = append(gaps, cannotInstall...)
	if len(gaps) == 0 && listErr == nil && recordErr == nil && marketplaceErr == nil && cacheErr == nil {
		fmt.Fprintln(stdout, "doctor: claude_plugins ok")
		return
	}
	printClaudePluginGaps(stdout, gaps, tally, failing)
}

// undecidedCopyFix is the repair of a bare name whose source clone or checkout could not be read.
const undecidedCopyFix = "repair the path named, or run pfm install --yes from inside your Professor clone to record it again"

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

// listedPluginGaps is the gaps an -alpha build fails on: each naming a plugin
// of ours claude.plugins lists, which must run from its -dev copy, enabled. On
// a release no gap fails.
func listedPluginGaps(version string, targets []installer.ClaudePluginTarget) func(installer.ClaudePluginGap) bool {
	var listed []string
	if obs.AlphaBuild(version) {
		for index := range targets {
			if targets[index].Own && !targets[index].InstallOnly {
				listed = append(listed, targets[index].Name)
			}
		}
	}
	return func(gap installer.ClaudePluginGap) bool {
		return slices.ContainsFunc(
			listed,
			func(name string) bool { return installer.ClaudePluginGapNames(gap.Problem, name) },
		)
	}
}

// printClaudePluginGaps prints one line per gap, its problem and its repair:
// a failure, marked FAIL, where failing says so, else a warning.
func printClaudePluginGaps(
	stdout io.Writer,
	gaps []installer.ClaudePluginGap,
	tally *doctorTally,
	failing func(installer.ClaudePluginGap) bool,
) {
	for _, gap := range gaps {
		mark := ""
		if failing(gap) {
			tally.fail()
			mark = "FAIL "
		} else {
			tally.warn()
		}
		fmt.Fprintf(stdout, "doctor: claude_plugins %s%s — %s\n", mark, gap.Problem, gap.Fix)
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
