package installer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// claudeConfigDirEnv is the variable that points a claude process at one
// account's config directory.
const claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// ConfigureClaudePlugins hands the plugin and env steps the machine config's
// claude.plugins, whether it is the default list, and claude.env as pfm writes it.
func ConfigureClaudePlugins(options *Options, machine pfmconfig.Config) {
	options.ClaudePlugins = machine.Claude.Plugins
	env := options.Env
	if env == nil {
		env = paths.OSEnv{}
	}
	options.ClaudeEnv = machine.EffectiveClaudeEnv(env, options.Home)
	options.ClaudePluginsDefault = machine.Source(pfmconfig.KeyClaudePlugins) == pfmconfig.SourceDefault
}

// claudePluginCommand is the claude subcommand every plugin command runs under.
const claudePluginCommand = "plugin"

// claudeMarketplaceCommand is the `claude plugin` subcommand over marketplaces.
const claudeMarketplaceCommand = "marketplace"

// claudePluginCommandTimeout bounds each `claude plugin` command: a plugin
// command that hangs (a network wait, a prompt) must not stall pfm install.
const claudePluginCommandTimeout = 2 * time.Minute

// claudePluginWaitDelay bounds the wait for a command's output pipes after it
// is killed, so a grandchild still holding them cannot stall Run.
const claudePluginWaitDelay = 5 * time.Second

// claudePluginTimeout is the per-command deadline in force. It is a variable
// only as a test seam: a test shortens it and restores it in t.Cleanup.
var claudePluginTimeout = claudePluginCommandTimeout

// ClaudePluginRepair is the repair every plugin gap pfm install closes names.
const ClaudePluginRepair = "run pfm install --yes"

// ClaudePluginUnresolvedFix is the repair for a listed plugin pfm cannot install.
const ClaudePluginUnresolvedFix = `give its claude.plugins entry a "source" (what claude plugin marketplace add takes), ` +
	`or run claude plugin marketplace add <source>, then run pfm install --yes`

// ClaudePluginBuild is what decides where each plugin comes from: pfm's own
// stamped version, the home pfm's paths resolve from, claude.pluginCheckoutRoot,
// and the source clone whose plugins/ is the checkout root when that key is
// unset (SourceRepoErr when the clone could not be resolved).
type ClaudePluginBuild struct {
	Version       string
	Home          string
	CheckoutRoot  string
	SourceRepo    string
	SourceRepoErr error
	Plugins       []pfmconfig.ClaudePlugin
	// PluginsDefault says claude.plugins is unset: an -alpha build then plans
	// every plugin the source clone's marketplace lists (WithMarketplacePlugins).
	PluginsDefault bool
	// Unlisted is every plugin the source clone's marketplace lists that
	// claude.plugins names in no form: an -alpha build installs each from its
	// checkout and keeps every copy disabled (WithMarketplacePlugins).
	Unlisted []string
}

// ClaudePluginTarget is one claude.plugins entry as this build ensures it. ID
// is the enabledPlugins key ensured and Source what `plugin marketplace add`
// takes, empty for a full id whose entry has no source. A bare name (Own) is
// {name}@professor on every build: from the GitHub professor marketplace on a
// release, or on an -alpha build with a checkout (Local) from the pfm-owned
// directory marketplace Marketplace, holding a copy of Checkout. Fallback says
// why an -alpha build has no checkout to install from: it then installs
// nothing, since one professor marketplace has one source. Undecided says why
// an -alpha build could not tell whether a checkout exists (its source clone
// or checkout could not be read): install and doctor then decide nothing,
// never reading the error as a missing checkout.
type ClaudePluginTarget struct {
	Name        string
	ID          string
	Source      string
	Local       bool
	Marketplace string
	Checkout    string
	Fallback    string
	Undecided   string
	// Own marks a bare name of ours.
	Own bool
	// MarketplaceName is the marketplace a full id names; empty for a bare name.
	MarketplaceName string
	// InstallOnly marks an -alpha marketplace plugin claude.plugins does not
	// name: installed from its checkout, kept disabled.
	InstallOnly bool
}

// NeedsKnownMarketplace reports whether the target installs only from a
// marketplace Claude Code already knows: a full id whose entry has no source.
func (target ClaudePluginTarget) NeedsKnownMarketplace() bool {
	return target.Source == "" && target.MarketplaceName != ""
}

// ClaudePluginTargets plans every claude.plugins entry for build. A bare name is
// a plugin of ours, {name}@professor: from the GitHub professor marketplace, or
// on an -alpha build its local copy. A full id is ensured as named, from the
// entry's source when it has one. An -alpha build then plans each of
// build.Unlisted as a bare name kept disabled (InstallOnly).
func ClaudePluginTargets(build ClaudePluginBuild) []ClaudePluginTarget {
	alpha := obs.AlphaBuild(build.Version)
	targets := make([]ClaudePluginTarget, 0, len(build.Plugins)+len(build.Unlisted))
	for _, plugin := range build.Plugins {
		if !plugin.Bare() {
			targets = append(targets, ClaudePluginTarget{
				Name:            plugin.Name(),
				ID:              plugin.ID,
				Source:          plugin.Source,
				MarketplaceName: plugin.Marketplace(),
			})
			continue
		}
		targets = append(targets, ownClaudePluginTarget(plugin.ID, build, alpha))
	}
	if alpha {
		for _, name := range build.Unlisted {
			target := ownClaudePluginTarget(name, build, alpha)
			target.InstallOnly = true
			targets = append(targets, target)
		}
	}
	return targets
}

// ownClaudePluginTarget plans the bare name of a plugin of ours.
func ownClaudePluginTarget(name string, build ClaudePluginBuild, alpha bool) ClaudePluginTarget {
	target := ClaudePluginTarget{
		Name:   name,
		ID:     name + "@" + pfmconfig.ProfessorMarketplace,
		Source: professorMarketplaceRepository,
		Own:    true,
	}
	if !alpha {
		return target
	}
	return localClaudePluginTarget(target, build)
}

// BareClaudePluginRepair is the repair install's FAIL line (and pfm doctor)
// names when a bare claude.plugins name could not be installed: pfm cannot know
// offline whether the professor marketplace lists the name.
func BareClaudePluginRepair(name string) string {
	return fmt.Sprintf(
		"claude.plugins entry %q is a bare name: pfm installs it from the professor marketplace github.com/%s — "+
			"fix or remove the entry in claude.plugins if that marketplace does not list it",
		name, professorMarketplaceRepository,
	)
}

// FallbackNote is the one line install and doctor print for a Fallback: with
// no checkout an -alpha build installs nothing.
func (target ClaudePluginTarget) FallbackNote() string {
	if target.InstallOnly {
		return target.Name + ": not installed — " + pfmconfig.KeyClaudePlugins +
			" does not list it and this alpha build has no checkout of it: " + target.Fallback
	}
	return target.Name + ": not installed — this alpha build has no checkout of it: " + target.Fallback
}

// ClaudePluginFallbackGap is a Fallback as the gap an -alpha install fails a
// listed plugin on and doctor reports.
func ClaudePluginFallbackGap(target ClaudePluginTarget) ClaudePluginGap {
	reason := target.Fallback
	if target.InstallOnly {
		reason = pfmconfig.KeyClaudePlugins + " does not list it and " + reason
	}
	return ClaudePluginGap{
		Problem: "plugin " + target.Name + " is not installed on this alpha build: " + reason,
		Fix: "check the plugin out where named, or set " + pfmconfig.KeyClaudePluginCheckoutRoot + ", then " +
			ClaudePluginRepair,
	}
}

// ClaudePluginGapNames reports whether problem is about the plugin name: it
// opens "plugin {name}" followed by a space, an "@" or a colon.
func ClaudePluginGapNames(problem, name string) bool {
	rest, ok := strings.CutPrefix(problem, "plugin "+name)
	return ok && rest != "" && strings.ContainsRune(" @:", rune(rest[0]))
}

// UndecidedNote names why an Undecided target's checkout is not decided.
func (target ClaudePluginTarget) UndecidedNote() string {
	return "its checkout is undecided: " + target.Undecided
}

func pluginEnabled(document map[string]any, id string) bool {
	enabled, _ := document["enabledPlugins"].(map[string]any)
	return enabled[id] == true
}

// claudePluginBuild is the build the plugin step plans from: claude.plugins
// plus, on an -alpha build, every plugin the marketplace lists.
func (installer *engine) claudePluginBuild() (ClaudePluginBuild, error) {
	// The clone the global links anchor on is the one whose plugins/ an
	// -alpha build copies from when claude.pluginCheckoutRoot is unset.
	sourceRepo, sourceRepoErr := installer.globalSourceRepoRoot()
	return ClaudePluginBuild{
		Version:        installer.options.Version,
		Home:           installer.options.Home,
		CheckoutRoot:   installer.options.ClaudePluginCheckoutRoot,
		SourceRepo:     sourceRepo,
		SourceRepoErr:  sourceRepoErr,
		Plugins:        installer.options.ClaudePlugins,
		PluginsDefault: installer.options.ClaudePluginsDefault,
	}.WithMarketplacePlugins()
}

// ensureClaudePlugins installs the shared plugins through the primary account.
// On a roster host its dir passes the refusal every launch applies, so the
// Options.ConfigDir fallback (the store) never runs. A host with no roster,
// whose Options.ConfigDir is the store, skips the step with one named line: no
// claude subprocess runs on the store (it would write .claude.json and backups
// there). A --config-dir on a roster host carries no roster but ClaudeRosterHost, so the same refusal holds.
// An -alpha build ensures each bare name with a checkout from its local copy
// (ClaudePluginTargets), plus every other plugin the source clone's
// marketplace lists, installed and kept disabled (WithMarketplacePlugins);
// every build then disables each enabled plugin id the retired lists name
// (retired.Lists ClaudePlugins) once its successor is ensured. A bare name
// whose checkout is undecided, or a listed one with no checkout on an -alpha build, is a failure naming why, left as it is. A listed id pfm cannot install is a failure naming it; an id the
// list does not name is never read or written. An applied -alpha install ends
// by reading every listed plugin of ours back: one not installed from its
// checkout copy and enabled is a failure (checkListedClaudePlugins).
func (installer *engine) ensureClaudePlugins(ctx context.Context) error {
	dir := installer.options.PrimaryConfigDir
	if dir == "" {
		dir = installer.options.ConfigDir
	}
	if len(installer.options.ClaudeAccounts) > 0 || installer.options.ClaudeRosterHost {
		if err := installer.checkLaunchConfigDir(0, dir); err != nil {
			return installer.pluginStepFailure(dir, err)
		}
	} else if claudelaunch.InspectConfigDir(
		claudelaunch.ClaudeStore(installer.options.Home), dir,
	).State == claudelaunch.ConfigDirStore {
		installer.skip(fmt.Sprintf(
			"claude plugins: not run — no account roster, so the only config dir is the Claude store %s, where a claude process writes account identity; add an account to pfm.config.json and rerun pfm install --yes",
			dir,
		))
		return nil
	}
	run := &claudePluginRun{dir: dir, settingsPath: filepath.Join(dir, "settings.json")}
	build, listErr := installer.claudePluginBuild()
	if listErr != nil {
		run.failures = append(run.failures, installer.pluginStepFailure(dir, listErr))
	}
	if lists, err := installer.retiredLists(); err == nil {
		// A failed read is deferred once by install; without the list no plugin is retired.
		run.retiredPlugins = lists.ClaudePlugins
	}
	targets := ClaudePluginTargets(build)
	var err error
	run.document, err = claudeplugin.ReadSettings(run.settingsPath)
	if err != nil {
		return errors.Join(append(run.failures,
			installer.pluginStepFailure(dir, fmt.Errorf("settings unreadable, state unknown: %w", err)))...)
	}
	run.missing, err = ClaudePluginsNotInstalled(dir, targets)
	if err != nil {
		return errors.Join(append(run.failures,
			installer.pluginStepFailure(dir, fmt.Errorf("install record unreadable, state unknown: %w", err)))...)
	}
	// An unreadable marketplace record fails only the ids that need it, below.
	run.unresolved, run.unresolvedErr = ClaudePluginsUnresolved(dir, targets)
	run.binary, run.resolveErr = ResolveClaudeBinary(
		installer.options.Home,
		installer.options.ClaudeBinary,
		installer.env().Get("PATH"),
	)
	failed := map[int]bool{}
	for index := range targets {
		before := len(run.failures)
		installer.ensureClaudePluginTarget(ctx, run, &targets[index])
		failed[index] = len(run.failures) > before
	}
	if installer.apply && obs.AlphaBuild(installer.options.Version) {
		installer.checkListedClaudePlugins(run, targets, failed)
	}
	return errors.Join(run.failures...)
}

// claudePluginRun is one ensureClaudePlugins pass: the account dir it runs
// through, what it read before the first plugin, and its failures.
type claudePluginRun struct {
	dir           string
	settingsPath  string
	document      map[string]any
	missing       []string
	unresolved    map[string]ClaudePluginGap
	unresolvedErr error
	binary        string
	resolveErr    error
	failures      []error
	// retiredPlugins is the retired lists' claudePlugins: plugin ids once ours,
	// keyed by their successor's bare name; nil when the lists could not be read.
	retiredPlugins map[string][]string
	// refreshed is each marketplace this pass refreshed, with the refresh's failure.
	refreshed map[string]error
}

// ensureClaudePluginTarget ensures one target, appending each failure to run.
func (installer *engine) ensureClaudePluginTarget(
	ctx context.Context,
	run *claudePluginRun,
	target *ClaudePluginTarget,
) {
	fail := func(err error) { run.failures = append(run.failures, installer.pluginFailure(run.dir, target.ID, err)) }
	if successor := target.retiredSuccessor(run.retiredPlugins); successor != "" {
		// Nothing is installed, enabled or disabled for it: the successor's own
		// pass disables its enabled copies.
		gap := retiredListedGap(target.Name, successor)
		fail(fmt.Errorf("%s — %s", gap.Problem, gap.Fix))
		return
	}
	if target.Undecided != "" {
		// No copy is installed, enabled or disabled on an error.
		run.failures = append(run.failures, installer.pluginFailure(
			run.dir, target.Name, errors.New(target.UndecidedNote()+"; left as it is"),
		))
		return
	}
	if target.Fallback != "" && !target.InstallOnly {
		// Nothing is installed or disabled.
		gap := ClaudePluginFallbackGap(*target)
		fail(fmt.Errorf("%s — %s", gap.Problem, gap.Fix))
		return
	}
	if gap, ok := run.unresolved[target.ID]; ok {
		fail(fmt.Errorf("%s — %s", gap.Problem, gap.Fix))
		return
	}
	installed := !slices.Contains(run.missing, target.ID)
	// An install-only plugin is ensured once installed: enabling it is no part of it.
	ensured := installed && (target.InstallOnly || pluginEnabled(run.document, target.ID))
	if run.unresolvedErr != nil && target.NeedsKnownMarketplace() && !ensured {
		fail(fmt.Errorf("marketplace record unreadable, state unknown: %w", run.unresolvedErr))
		return
	}
	if target.Local {
		if _, binaryPlugin := goBinaryPlugins[target.Name]; binaryPlugin && !target.InstallOnly {
			// The copy's hooks run the binary of its version: it goes first.
			if err := installer.ensureGoPluginBinary(ctx, target); err != nil {
				fail(err)
				return
			}
		}
		if err := installer.ensureClaudePluginCopy(target); err != nil {
			fail(err)
			return
		}
	}
	if target.Own && target.Source != "" && ensured {
		// An install below runs the same add; an installed copy needs its own.
		if err := installer.repointClaudePluginMarketplace(ctx, run, target); err != nil {
			fail(err)
			return
		}
	}
	switch {
	case ensured && target.InstallOnly:
		line := "claude plugin " + target.ID + " installed in " + run.dir
		// A copy found enabled is disabled below: never called kept disabled.
		if !pluginEnabled(run.document, target.ID) {
			line += ", kept disabled: " + pfmconfig.KeyClaudePlugins + " does not list " + target.Name
		}
		installer.ok(line)
	case ensured:
		installer.ok("claude plugin " + target.ID + " installed and enabled in " + run.dir)
	case run.resolveErr != nil:
		installer.skip(fmt.Sprintf(
			"claude plugin %s in %s: real claude binary not resolved: %v", target.ID, run.dir, run.resolveErr,
		))
		return
	case target.Fallback != "":
		// Unlisted with no checkout: not installed, and a copy found enabled is
		// still disabled below.
		installer.skip("claude plugin " + target.FallbackNote())
	default:
		if target.Own && !target.Local {
			installer.refreshClaudePluginMarketplace(ctx, run, target)
			if installed {
				// A disabled record may hold any older copy: the install would enable it as it is.
				if err := installer.updateClaudePluginRecord(ctx, run, target); err != nil {
					fail(err)
					return
				}
			}
		}
		if err := installer.installClaudePlugin(ctx, run.binary, run.dir, target); err != nil {
			if target.Own && !target.Local {
				err = fmt.Errorf("%w — %s", err, run.ownPluginRepair(target))
			}
			fail(err)
			return
		}
		// An applied install snapshots the user scope; another scope may still be behind.
		installed = installed || installer.apply
		if target.InstallOnly && !installer.apply {
			// The install enables it and the edit after it disables it; a preview
			// reads settings from before the install, so it names that edit here.
			_ = installer.change("disable "+target.ID+" in "+run.settingsPath+": "+
				pfmconfig.KeyClaudePlugins+" does not list "+target.Name, nil)
		}
	}
	if target.Local && installed {
		// Claude records an installed copy's version, which moves only here and
		// at the install above.
		if err := installer.ensureClaudePluginCache(ctx, run, target); err != nil {
			fail(err)
		}
	}
	if !target.Own {
		// A listed full id is ensured as named; its settings entries are all
		// that is touched, and no other id is its twin to retire.
		return
	}
	// settle runs one planned settings.json edit, reporting whether the next may follow.
	settle := func(message string, edit func() error, err error) bool {
		if err == nil && message != "" {
			err = installer.change(message, edit)
		}
		if err != nil {
			fail(err)
		}
		return err == nil
	}
	if settle(planInstallOnlyDisable(run.settingsPath, target)) {
		settle(planRetiredPluginDisable(run.settingsPath, target, run.retiredPlugins[target.Name]))
	}
}

// refreshClaudePluginMarketplace runs `claude plugin marketplace update
// {marketplace}` once per pass, before the first install of a plugin of ours a
// release build takes from GitHub, when Claude Code already knows that
// marketplace: its cached listing may predate a plugin this release adds or
// renames, and the `plugin marketplace add` the install runs refreshes nothing
// on a known name. A marketplace not yet known is fetched fresh by that add. A
// failed refresh is a warning, never a failure by itself: the install that
// follows is the judge, and its repair names the refresh (ownPluginRepair).
func (installer *engine) refreshClaudePluginMarketplace(
	ctx context.Context,
	run *claudePluginRun,
	target *ClaudePluginTarget,
) {
	_, marketplace, _ := strings.Cut(target.ID, "@")
	if _, done := run.refreshed[marketplace]; done {
		return
	}
	// An unreadable record cannot say the marketplace is unknown, so the refresh still runs.
	if _, known, err := claudeplugin.ReadKnownMarketplaces(run.dir); err == nil {
		if _, ok := known[marketplace]; !ok {
			return
		}
	}
	if run.refreshed == nil {
		run.refreshed = map[string]error{}
	}
	update := []string{run.binary, claudePluginCommand, claudeMarketplaceCommand, "update", marketplace}
	message := fmt.Sprintf("run %s=%s %s: Claude Code's listing of marketplace %s may predate this release",
		claudeConfigDirEnv, run.dir, shellCommandLine(update...), marketplace)
	err := installer.change(message, func() error {
		options := deps.RunOptions{Env: pluginCommandEnvironment(run.dir)}
		return runClaudePluginCommand(ctx, installer.processRunner(), update, options)
	})
	run.refreshed[marketplace] = err
	if err != nil {
		warning := fmt.Sprintf("claude plugin marketplace %s in %s not refreshed: %v", marketplace, run.dir, err)
		installer.say("  warn    %s", warning)
		installer.record("warn", warning, err)
	}
}

// ownPluginRepair is the repair a failed install of a plugin of ours from
// GitHub names: a marketplace whose refresh failed may still list the
// plugins from before this release, so the refresh is the repair; one
// refreshed or freshly added lists what GitHub does, so a name it lacks is
// the claude.plugins entry's.
func (run *claudePluginRun) ownPluginRepair(target *ClaudePluginTarget) string {
	_, marketplace, _ := strings.Cut(target.ID, "@")
	if run.refreshed[marketplace] == nil {
		return BareClaudePluginRepair(target.Name)
	}
	return fmt.Sprintf("Claude Code's listing of marketplace %s may predate %s and could not be refreshed: "+
		"run %s=%s %s, then pfm install --yes", marketplace, target.Name, claudeConfigDirEnv, run.dir,
		shellCommandLine(run.binary, claudePluginCommand, claudeMarketplaceCommand, "update", marketplace))
}

// updateClaudePluginRecord brings a disabled install record of a plugin of
// ours a release build takes from GitHub to the marketplace's current version
// before the install enables it: the record may be any older copy (a retired
// plugin's id now its successor's), and the version the marketplace serves
// is known only to Claude Code. One `claude plugin update` runs per scope.
func (installer *engine) updateClaudePluginRecord(
	ctx context.Context,
	run *claudePluginRun,
	target *ClaudePluginTarget,
) error {
	record, err := claudeplugin.ReadInstalls(run.dir)
	if err != nil {
		return err
	}
	_, failures := installer.updateClaudePluginEntries(ctx, run, target.ID, record[target.ID],
		func(entry claudeplugin.Install) string {
			recorded := "at version " + EscapeControl(entry.Version)
			if entry.Version == "" {
				recorded = "with no version string"
			}
			return "plugin " + target.ID + " is disabled " + recorded +
				", brought to its marketplace's current version before it is enabled"
		})
	return errors.Join(failures...)
}

// repointClaudePluginMarketplace re-points the professor marketplace of an
// installed plugin of ours that Claude Code knows from another source at the
// target's own source, its checkout copies' dir on -alpha: `plugin
// marketplace add` on a known name re-points it and keeps its plugins and their
// options, which `plugin marketplace remove` would delete. An applied re-point
// is read back, and a marketplace still pointing elsewhere is an error.
func (installer *engine) repointClaudePluginMarketplace(
	ctx context.Context,
	run *claudePluginRun,
	target *ClaudePluginTarget,
) error {
	one := []ClaudePluginTarget{*target}
	gaps, err := ClaudePluginMarketplaceGaps(run.dir, one)
	if err != nil || len(gaps) == 0 {
		return err
	}
	if run.resolveErr != nil {
		return fmt.Errorf("%s, and the real claude binary that re-points it is not resolved: %w",
			gaps[0].Problem, run.resolveErr)
	}
	add := []string{run.binary, claudePluginCommand, claudeMarketplaceCommand, "add", target.Source}
	message := fmt.Sprintf("run %s=%s %s: %s", claudeConfigDirEnv, run.dir, shellCommandLine(add...), gaps[0].Problem)
	return installer.change(message, func() error {
		options := deps.RunOptions{Env: pluginCommandEnvironment(run.dir)}
		if err := runClaudePluginCommand(ctx, installer.processRunner(), add, options); err != nil {
			return err
		}
		after, err := ClaudePluginMarketplaceGaps(run.dir, one)
		if err != nil {
			return err
		}
		if len(after) != 0 {
			_, marketplace, _ := strings.Cut(target.ID, "@")
			return fmt.Errorf("%s after the add — %s", after[0].Problem, claudeMarketplaceRemoveFix(marketplace))
		}
		return nil
	})
}

// checkListedClaudePlugins reads back, after an applied -alpha pass, every
// plugin of ours claude.plugins lists that the pass did not already fail: one
// not installed from its checkout copy and enabled is a failure naming each
// problem pfm doctor reports for it.
func (installer *engine) checkListedClaudePlugins(
	run *claudePluginRun,
	targets []ClaudePluginTarget,
	failed map[int]bool,
) {
	for index := range targets {
		target := targets[index]
		if !target.Own || target.InstallOnly || failed[index] {
			continue
		}
		problems, err := listedClaudePluginProblems(run.dir, target, run.retiredPlugins)
		if err != nil {
			problems = append(problems, "state unknown: "+err.Error())
		}
		if len(problems) == 0 {
			continue
		}
		text := "not installed from its checkout copy and enabled after this install: " + strings.Join(problems, "; ")
		run.failures = append(run.failures, installer.pluginFailure(run.dir, target.ID, errors.New(text)))
	}
}

// listedClaudePluginProblems is every problem of target in the account dir:
// its fallback, and each settings, install-record and marketplace gap naming it.
func listedClaudePluginProblems(
	dir string,
	target ClaudePluginTarget,
	retiredPlugins map[string][]string,
) ([]string, error) {
	one := []ClaudePluginTarget{target}
	var problems []string
	if target.Fallback != "" {
		problems = append(problems, ClaudePluginFallbackGap(target).Problem)
	}
	settings := filepath.Join(dir, "settings.json")
	gaps, err := ClaudePluginGaps(settings, one, retiredPlugins)
	if errors.Is(err, ErrClaudeSettingsAbsent) {
		// No settings.json: nothing is enabled.
		gaps = []ClaudePluginGap{{Problem: "plugin " + target.ID + " not enabled in " + settings + " (no such file)"}}
	} else if err != nil {
		return problems, err
	}
	marketplaceGaps, err := ClaudePluginMarketplaceGaps(dir, one)
	if err != nil {
		return problems, err
	}
	missing, err := ClaudePluginsNotInstalled(dir, one)
	if err != nil {
		return problems, err
	}
	for _, id := range missing {
		problems = append(problems, "plugin "+id+" not installed in "+dir)
	}
	for _, gap := range append(gaps, marketplaceGaps...) {
		if ClaudePluginGapNames(gap.Problem, target.Name) {
			problems = append(problems, gap.Problem)
		}
	}
	return problems, nil
}

func (installer *engine) installClaudePlugin(
	ctx context.Context,
	binary, dir string,
	target *ClaudePluginTarget,
) error {
	install := []string{binary, claudePluginCommand, "install", target.ID, "-y"}
	var add []string
	message := fmt.Sprintf("run %s=%s %s", claudeConfigDirEnv, dir, shellCommandLine(install...))
	if target.Source != "" {
		add = []string{binary, claudePluginCommand, claudeMarketplaceCommand, "add", target.Source}
		message = fmt.Sprintf(
			"run %s=%s %s && %s", claudeConfigDirEnv, dir, shellCommandLine(add...), shellCommandLine(install...),
		)
	}
	return installer.change(message, func() error {
		options := deps.RunOptions{Env: pluginCommandEnvironment(dir)}
		var addErr error
		if add != nil {
			// An already-added marketplace exits non-zero; the install that
			// follows is the judge, so its failure carries this answer too.
			addErr = runClaudePluginCommand(ctx, installer.processRunner(), add, options)
		}
		if err := runClaudePluginCommand(ctx, installer.processRunner(), install, options); err != nil {
			if addErr != nil {
				return errors.Join(addErr, err)
			}
			return err
		}
		return nil
	})
}

// pluginCommandEnvironment is the inherited environment with CLAUDE_CONFIG_DIR
// set to dir and the login default's sentinel dropped: the child runs on the
// dir pfm chose, so nothing may read it as the inherited default.
func pluginCommandEnvironment(dir string) []string {
	inherited := deps.EnvironmentWith(claudeConfigDirEnv, dir)
	environment := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		if !strings.HasPrefix(entry, claudelaunch.ConfigDirDefaultEnv+"=") {
			environment = append(environment, entry)
		}
	}
	return environment
}

func runClaudePluginCommand(ctx context.Context, runner deps.Runner, argv []string, options deps.RunOptions) error {
	return runPluginCommandWithin(ctx, runner, argv, options, claudePluginTimeout)
}

// runPluginCommandWithin runs argv in its own process group under timeout,
// any failure an error naming the command, its exit code and its stderr.
func runPluginCommandWithin(
	ctx context.Context,
	runner deps.Runner,
	argv []string,
	options deps.RunOptions,
	timeout time.Duration,
) error {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	options.ProcessGroup = true
	options.WaitDelay = claudePluginWaitDelay
	result, err := runner.Run(cmdCtx, argv, options)
	if cmdCtx.Err() != nil && (err != nil || result.ExitCode != 0) {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: cancelled: %w", strings.Join(argv[1:], " "), ctx.Err())
		}
		return fmt.Errorf(
			"%s: timed out after %s: %w", strings.Join(argv[1:], " "), timeout, cmdCtx.Err(),
		)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv[1:], " "), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(
			"%s: exit %d stderr=%q",
			strings.Join(argv[1:], " "),
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	return nil
}

func (installer *engine) pluginFailure(dir, plugin string, err error) error {
	return installer.fail(fmt.Errorf("claude plugin %s in %s: %w", plugin, dir, err))
}

func (installer *engine) pluginStepFailure(dir string, err error) error {
	return installer.fail(fmt.Errorf("claude plugins in %s: %w", dir, err))
}
