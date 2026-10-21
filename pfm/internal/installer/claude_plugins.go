package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// claudeConfigDirEnv is the variable that points a claude process at one
// account's config directory.
const claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// claudePluginCommand is the claude subcommand every plugin command runs under.
const claudePluginCommand = "plugin"

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
// takes, empty for a full id whose entry has no source. For a bare name (Own):
// the GitHub repository and GitHubID on a release, or on an -alpha build with
// a checkout (Local) the pfm-owned marketplace dir Marketplace, holding a copy
// of Checkout, and DevID. Fallback says why an -alpha build ensures the GitHub
// copy instead; empty on a release. Undecided says why an -alpha build could
// not tell whether a checkout exists (its source clone or checkout could not
// be read): install and doctor then decide neither copy, never reading the
// error as a missing checkout.
type ClaudePluginTarget struct {
	Name        string
	ID          string
	GitHubID    string
	DevID       string
	Source      string
	Local       bool
	Marketplace string
	Checkout    string
	Fallback    string
	Undecided   string
	// Own marks a bare name of ours, which has a GitHub copy and a -dev copy.
	Own bool
	// MarketplaceName is the marketplace a full id names; empty for a bare name.
	MarketplaceName string
	// ProfessorID is a bare name's id in the professor marketplace,
	// {name}@professor: a third copy of ours, which every build retires like
	// its other copies; empty when claude.plugins lists that id.
	ProfessorID string
	// InstallOnly marks an -alpha marketplace plugin claude.plugins does not
	// name: installed from its checkout, every copy kept disabled.
	InstallOnly bool
}

// NeedsKnownMarketplace reports whether the target installs only from a
// marketplace Claude Code already knows: a full id whose entry has no source.
func (target ClaudePluginTarget) NeedsKnownMarketplace() bool {
	return target.Source == "" && target.MarketplaceName != ""
}

// ClaudePluginTargets plans every claude.plugins entry for build. A bare name is
// a plugin of ours: its GitHub repository rezzminator/{name} with the id
// {name}@{name}, or on an -alpha build with a checkout its local -dev copy. A
// full id is ensured as named, from the entry's source when it has one. An
// -alpha build then plans each of build.Unlisted as a bare name kept disabled
// (InstallOnly).
func ClaudePluginTargets(build ClaudePluginBuild) []ClaudePluginTarget {
	alpha := obs.AlphaBuild(build.Version)
	targets := make([]ClaudePluginTarget, 0, len(build.Plugins)+len(build.Unlisted))
	listed := map[string]bool{}
	for _, plugin := range build.Plugins {
		listed[plugin.ID] = true
	}
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
		targets = append(targets, ownClaudePluginTarget(plugin.ID, build, alpha, listed))
	}
	if alpha {
		for _, name := range build.Unlisted {
			target := ownClaudePluginTarget(name, build, alpha, listed)
			target.InstallOnly = true
			targets = append(targets, target)
		}
	}
	return targets
}

// ownClaudePluginTarget plans the bare name of a plugin of ours. Its
// professor-marketplace id is a third copy, unless claude.plugins lists that
// id itself.
func ownClaudePluginTarget(
	name string,
	build ClaudePluginBuild,
	alpha bool,
	listed map[string]bool,
) ClaudePluginTarget {
	id := name + "@" + name
	target := ClaudePluginTarget{
		Name:     name,
		ID:       id,
		GitHubID: id,
		DevID:    name + "@" + devMarketplaceName(name),
		Source:   claudePluginRepository(name),
		Own:      true,
	}
	if professor := name + "@" + professorMarketplaceName; !listed[professor] {
		target.ProfessorID = professor
	}
	if !alpha {
		return target
	}
	return localClaudePluginTarget(target, build)
}

// claudePluginRepository keeps the renamed identity on its existing public repository.
func claudePluginRepository(name string) string {
	if name == "auto-effort-control" {
		return claudePluginOwner + "/auto-effort" // Migration exception until an authorized repository rename.
	}
	return claudePluginOwner + "/" + name
}

// BareClaudePluginRepair is the repair install's FAIL line (and pfm doctor)
// names when a bare claude.plugins name could not be installed: pfm cannot know
// offline whether the GitHub repository the name stands for exists.
func BareClaudePluginRepair(name string) string {
	return fmt.Sprintf(
		"claude.plugins entry %q is a bare name: pfm installs it from github.com/%s — "+
			"fix or remove the entry in claude.plugins if that repository does not exist",
		name, claudePluginRepository(name),
	)
}

// FallbackNote is the one line install and doctor print for a Fallback. An
// install-only plugin never falls back: with no checkout it is not installed.
func (target ClaudePluginTarget) FallbackNote() string {
	if target.InstallOnly {
		return target.Name + ": not installed — " + pfmconfig.KeyClaudePlugins +
			" does not list it and this alpha build has no checkout of it: " + target.Fallback
	}
	return target.Name + ": alpha build falls back to the GitHub copy " + target.GitHubID + " — " + target.Fallback
}

// ClaudePluginFallbackGap is a Fallback as the gap an -alpha install fails a
// listed plugin on and doctor reports.
func ClaudePluginFallbackGap(target ClaudePluginTarget) ClaudePluginGap {
	if target.InstallOnly {
		return ClaudePluginGap{
			Problem: "plugin " + target.Name + " is not installed on this alpha build: " +
				pfmconfig.KeyClaudePlugins + " does not list it and " + target.Fallback,
			Fix: "check the plugin out where named, or set " + pfmconfig.KeyClaudePluginCheckoutRoot + ", then " +
				ClaudePluginRepair,
		}
	}
	return ClaudePluginGap{
		Problem: "plugin " + target.Name + " falls back to the GitHub copy " + target.GitHubID +
			" on this alpha build: " + target.Fallback,
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

// UndecidedNote names an Undecided target's two copies and why neither is chosen.
func (target ClaudePluginTarget) UndecidedNote() string {
	return "its copy is undecided (" + target.GitHubID + " or " + target.DevID + "): " + target.Undecided
}

func readClaudeSettingsDocument(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if document == nil {
		document = map[string]any{}
	}
	return document, nil
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
// every build then disables the copies of ours it does not ensure. A bare name
// whose source is undecided is a failure naming the error, its copies left as
// they are. A listed id pfm cannot install is a failure naming it; an id the
// list does not name is never read or written. An applied -alpha install ends
// by reading every listed plugin of ours back: one not installed from its -dev
// copy and enabled is a failure (checkListedClaudePlugins).
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
	targets := ClaudePluginTargets(build)
	for index := range targets {
		target := &targets[index]
		if target.Fallback != "" {
			installer.skip("claude plugin " + target.FallbackNote())
		}
	}
	var err error
	run.document, err = readClaudeSettingsDocument(run.settingsPath)
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
}

// ensureClaudePluginTarget ensures one target, appending each failure to run.
func (installer *engine) ensureClaudePluginTarget(
	ctx context.Context,
	run *claudePluginRun,
	target *ClaudePluginTarget,
) {
	fail := func(err error) { run.failures = append(run.failures, installer.pluginFailure(run.dir, target.ID, err)) }
	if target.Undecided != "" {
		// No copy is installed, enabled or disabled on an error.
		run.failures = append(run.failures, installer.pluginFailure(
			run.dir, target.Name, errors.New(target.UndecidedNote()+"; both left as they are"),
		))
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
		if target.Name == callmeterPlugin && !target.InstallOnly {
			// The copy's hooks run the binary of its version: it goes first.
			if err := installer.ensureCallmeterBinary(ctx, target); err != nil {
				fail(err)
				return
			}
		}
		if err := installer.ensureClaudePluginCopy(target); err != nil {
			fail(err)
			return
		}
	}
	if target.Local && ensured {
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
		if !slices.ContainsFunc(target.copyIDs(), func(id string) bool { return pluginEnabled(run.document, id) }) {
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
	case target.InstallOnly && !target.Local:
		// Unlisted with no checkout: never installed from GitHub; its skip line
		// is printed above, and a copy found enabled is still disabled below.
	default:
		if err := installer.installClaudePlugin(ctx, run.binary, run.dir, target); err != nil {
			if target.Own && !target.Local {
				err = fmt.Errorf("%w — %s", err, BareClaudePluginRepair(target.Name))
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
		// Claude runs an installed copy from its snapshot, which moves only here
		// and at the install above.
		if err := installer.ensureClaudePluginCache(ctx, run, target); err != nil {
			fail(err)
		}
	}
	if !target.Own {
		// A listed full id is ensured as named; its settings entries are all
		// that is touched, and no other id is its twin to retire.
		return
	}
	for _, plan := range []func(string, *ClaudePluginTarget) (string, func() error, error){
		planOtherCopyRetirement, planRetiredPluginDisable,
	} {
		message, edit, err := plan(run.settingsPath, target)
		if err != nil {
			fail(err)
			return
		}
		if message == "" {
			continue
		}
		if err := installer.change(message, edit); err != nil {
			fail(err)
		}
	}
}

// repointClaudePluginMarketplace re-points an installed local target's
// marketplace that Claude Code knows from another source at the target's own
// dir: `plugin
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
	add := []string{run.binary, claudePluginCommand, "marketplace", "add", target.Source}
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
// not installed from its -dev copy and enabled is a failure naming each problem
// pfm doctor reports for it.
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
		problems, err := listedClaudePluginProblems(run.dir, target)
		if err != nil {
			problems = append(problems, "state unknown: "+err.Error())
		}
		if len(problems) == 0 {
			continue
		}
		text := "not installed from its -dev copy and enabled after this install: " + strings.Join(problems, "; ")
		run.failures = append(run.failures, installer.pluginFailure(run.dir, target.ID, errors.New(text)))
	}
}

// listedClaudePluginProblems is every problem of target in the account dir:
// its fallback, and each settings, install-record and marketplace gap naming it.
func listedClaudePluginProblems(dir string, target ClaudePluginTarget) ([]string, error) {
	one := []ClaudePluginTarget{target}
	var problems []string
	if target.Fallback != "" {
		problems = append(problems, ClaudePluginFallbackGap(target).Problem)
	}
	settings := filepath.Join(dir, "settings.json")
	gaps, err := ClaudePluginGaps(settings, one)
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
	for name, definition := range pfmconfig.ExtensionRegistry() {
		if installer.options.Extensions[name].Active(pfmengine.MustLookup(pfmengine.Claude).LongName) &&
			definition.Engines[pfmengine.MustLookup(pfmengine.Claude).LongName].ID == target.ID {
			install = append(
				install,
				definition.Engines[pfmengine.MustLookup(pfmengine.Claude).LongName].InstallSuffix...)
		}
	}
	var add []string
	message := fmt.Sprintf("run %s=%s %s", claudeConfigDirEnv, dir, shellCommandLine(install...))
	if target.Source != "" {
		add = []string{binary, claudePluginCommand, "marketplace", "add", target.Source}
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
