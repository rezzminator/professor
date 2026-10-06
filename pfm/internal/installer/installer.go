package installer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type engine struct {
	options     Options
	report      Report
	apply       bool
	stamp       string
	managedRoot string
	outputErr   error
	planErrors  []error
	deferred    []error
	// removedPaths are the paths this pass removed, or — in a dry run, where
	// nothing is removed at all — planned to remove. retireEmptyDir discounts
	// them before refusing a non-empty directory (retire_empty_dir.go).
	removedPaths map[string]bool
}

type pinnedHarvestProvisioner struct{}

func (pinnedHarvestProvisioner) Plan(platform harvestpy.Platform) (harvestpy.InstallPlan, error) {
	return harvestpy.PlanConversionEnvironment(platform)
}

func (pinnedHarvestProvisioner) Provision(
	ctx context.Context,
	options harvestpy.ProvisionOptions,
) (harvestpy.ProvisionResult, error) {
	return harvestpy.Provision(ctx, options)
}

func (pinnedHarvestProvisioner) Check(
	ctx context.Context,
	root string,
	platform harvestpy.Platform,
) (harvestpy.CheckReport, error) {
	return harvestpy.CheckConversionEnvironment(ctx, root, platform)
}

// NewHarvestProvisioner returns the production pinned-runtime adapter. Tests
// should inject their own HarvestProvisioner through Options instead.
func NewHarvestProvisioner() HarvestProvisioner { return pinnedHarvestProvisioner{} }

func Run(ctx context.Context, options Options) (report Report, err error) {
	endRun := runSpan(ctx, options.Mode)
	defer func() { endRun(err) }()
	options, err = normalizeInstallerOptions(options)
	if err != nil {
		return Report{}, fmt.Errorf("resolve installer options: %w", err)
	}
	if options.Mode > ModeUninstall {
		return Report{}, fmt.Errorf("unknown installer mode %d", options.Mode)
	}
	if options.Mode != ModeDryRun {
		// A job running now refuses before the installer writes.
		probed, gateErr := awaitSchedulerGate(ctx, options)
		if gateErr != nil {
			return Report{}, gateErr
		}
		if !probed && schedulerIsLaunchd {
			options.launchGateUnprobed = true
		} else if !probed {
			options.nameSyncGateUnprobed = true
		}
	}

	installer := &engine{
		options:     options,
		apply:       options.Mode != ModeDryRun,
		stamp:       options.Now().Format("20060102-150405"),
		managedRoot: managedRootForHome(options.Home),
	}
	installer.say("pfm install: home=%s config=%s", options.Home, options.ConfigDir)
	switch options.Mode {
	case ModeDryRun:
		installer.say("MODE: dry run — nothing will change")
	case ModeApply:
		installer.say("MODE: apply")
	case ModeUninstall:
		installer.say("MODE: uninstall")
	}
	installer.say("")
	if options.Mode != ModeDryRun {
		if err := installer.preflight(ctx, options.Mode); err != nil {
			return installer.report, err
		}
	}

	if options.Mode == ModeUninstall {
		err = installer.uninstall(ctx)
	} else {
		err = installer.install(ctx)
	}
	if len(installer.planErrors) != 0 {
		err = errors.Join(append([]error{err}, installer.planErrors...)...)
	}
	if len(installer.deferred) != 0 {
		err = errors.Join(append([]error{err}, installer.deferred...)...)
	}
	installer.say("")
	installer.say("summary changed=%d ok=%d skipped=%d", installer.report.Changed,
		installer.report.OK, installer.report.Skipped)
	if installer.outputErr != nil {
		err = errors.Join(err, installer.outputErr)
	}
	return installer.report, err
}

// fail prints and records a step failure and returns it.
func (installer *engine) fail(err error) error {
	installer.say("  FAIL    %s", err)
	installer.record("fail", err.Error(), err)
	return err
}

func (installer *engine) deferFailure(err error) {
	if err != nil {
		installer.deferred = append(installer.deferred, err)
	}
}

// preflight executes the selected complete planner against the same host
// snapshot immediately before mutation. Every planner stays read-only, but all
// conflicts and future paths are computed before install or uninstall can
// mutate the machine.
func (installer *engine) preflight(ctx context.Context, mode Mode) error {
	options := installer.options
	options.Stdout = io.Discard
	preview := &engine{
		options: options, apply: false, stamp: installer.stamp, managedRoot: installer.managedRoot,
	}
	var (
		planErr error
		label   string
	)
	switch mode {
	case ModeApply:
		preview.options.Mode = ModeDryRun
		planErr = preview.install(ctx)
		label = "preflight apply plan"
	case ModeUninstall:
		preview.options.Mode = ModeUninstall
		planErr = preview.uninstall(ctx)
		label = "preflight uninstall plan"
	default:
		return fmt.Errorf("preflight unknown installer mode %d", mode)
	}
	if mode == ModeApply && len(preview.deferred) != 0 {
		planErr = errors.Join(append([]error{planErr}, preview.deferred...)...)
	}
	if len(preview.planErrors) != 0 {
		planErr = errors.Join(append([]error{planErr}, preview.planErrors...)...)
	}
	if planErr != nil {
		return fmt.Errorf("%s: %w", label, planErr)
	}
	if preview.outputErr != nil {
		return fmt.Errorf("%s output: %w", label, preview.outputErr)
	}
	return nil
}

func (installer *engine) install(ctx context.Context) error {
	if err := installer.seedConfig(); err != nil {
		return err
	}
	installer.deferFailure(installer.installHarvest(ctx))
	installer.deferFailure(installer.installMarkdownTool(ctx))
	installer.installThemes(ctx)
	assets, err := assetFiles()
	if err != nil {
		return fmt.Errorf("enumerate embedded install assets: %w", err)
	}
	systemdAssetChanged, err := installer.stageAssets(assets)
	installer.deferFailure(err)
	installer.deferFailure(installer.installManagedCleanup(ctx))
	installer.deferFailure(installer.wireClaudeLauncher())
	installer.deferFailure(installer.pruneClaudeVersions())
	installer.deferFailure(installer.wireHostOverlays())
	installer.deferFailure(installer.migrateOldState())
	installer.deferFailure(installer.migrateLegacyCarrier(ctx))
	installer.deferFailure(installer.wireClaudeStore())
	installer.deferFailure(installer.retireStoreEntries())
	installer.deferFailure(installer.retirePredecessors())
	installer.deferFailure(installer.retireRenamedGlobalAgents())
	installer.deferFailure(installer.retireBBInstall())
	installer.deferFailure(installer.retireChatCommands())
	installer.deferFailure(installer.retireStagedManagedSurfaces(false))
	installer.deferFailure(installer.wireCommands(assets))
	installer.deferFailure(installer.wireSkills(assets))
	installer.deferFailure(installer.wireGlobalCommands())
	installer.deferFailure(installer.retireDeadRegistryLinks())
	installer.deferFailure(installer.wireGlobalSkills())
	installer.deferFailure(installer.retireLegacySwapCommand())
	installer.deferFailure(installer.wireCodexAgents())
	if len(installer.codexHomes()) == 0 {
		installer.skip("no Codex accounts configured — command mirror has nothing to write")
		installer.skip("no Codex accounts configured — agent mirror has nothing to write")
	} else {
		installer.deferFailure(installer.reconcileCodexCommands(assets))
		installer.deferFailure(installer.wireCodexDefaults())
		installer.deferFailure(installer.retireOrphanCodexAgents())
	}
	// The periodic name-sync has one job and two schedulers. Linux gets the
	// systemd units; macOS gets a launchd agent that carries both triggers.
	// Staging the other platform's files would leave an operator with a
	// ~/.config/systemd/user full of units nothing will ever read.
	if schedulerIsLaunchd {
		if installer.apply {
			if installer.options.launchGateUnprobed {
				installer.skip(launchGateUnprobedNote)
			} else {
				installer.ok("launch-agent gate: name-sync is not mid-execution")
			}
		}
		installer.deferFailure(installer.ensureLaunchdLogDir())
		installer.deferFailure(installer.wireLaunchAgent(ctx))
		installer.deferFailure(installer.wireReminderLaunchAgent(ctx))
		installer.deferFailure(installer.wireMCPLaunchAgent(ctx))
	} else {
		if installer.apply && installer.options.nameSyncGateUnprobed {
			installer.skip(nameSyncGateUnprobedNote)
		}
		unitChanged, err := installer.wireUnits(ctx)
		installer.deferFailure(err)
		reload := systemdAssetChanged || unitChanged
		if !reload && installer.apply {
			for _, unit := range []string{nameSyncPathUnit, nameSyncTimerUnit, reminderTimerUnit} {
				state, err := fleetUnitState(ctx, installer.options.Runner, unit)
				if err != nil || state != unitStateActive {
					reload = true
					break
				}
			}
		}
		if reload {
			installer.reloadUnits(ctx)
		}
	}
	// A failed plugin install is reported at once and fails the run only
	// after every later step has landed.
	installer.deferFailure(installer.ensureClaudePlugins(ctx))
	installer.deferFailure(installer.clearFullscreenAutoDisable())
	installer.deferFailure(installer.wireCodexHooks())
	if err := installer.removeRetiredNudgeState(); err != nil {
		installer.warnRetiredNudge("retired compact-nudge state", err)
	}
	mcpErr := installer.wireMCP()
	// A host build replaces the binary without changing the unit file, and MCP
	// client wiring can change without changing either. enable --now leaves an
	// already-running process untouched, so always restart the enabled Linux
	// daemon after its complete config/client transaction has landed.
	// A restart that does not come back fails the run after every later step
	// has landed, like a failed plugin install.
	if !schedulerIsLaunchd && installer.apply && installer.mcpAnyEnabled() && installer.userManagerAvailable(ctx) {
		installer.deferFailure(installer.restartMCPUnit(ctx))
	}
	installer.deferFailure(mcpErr)
	installer.deferFailure(installer.wireOpenCodeInstructions())
	installer.deferFailure(installer.wireLogDefault())
	installer.deferFailure(installer.wireShell(false))
	installer.deferFailure(installer.wireVSCode())
	installer.deferFailure(installer.writeUpdateMetadata())
	// Like a failed plugin install, a refused login default is reported at
	// once and fails the run after every other step has landed.
	installer.deferFailure(installer.wireLoginDefault(false))
	return nil
}

// wireCodexAgents runs on every install: it serves the Claude agent
// registries (links, orphaned-link and undeclared-variant retirement) whatever
// the codex roster, and compiles, writes and retires Codex roles only in the
// Codex homes configured — an empty roster plans none.
func (installer *engine) wireCodexAgents() error {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve Codex global agents source repository: %w", err)
	}
	source := filepath.Join(sourceRepo, "templates", "global", "agents")
	if _, err := os.Stat(source); errors.Is(err, fs.ErrNotExist) {
		installer.skip("Codex global agents source absent at " + source)
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect Codex global agents source %s: %w", source, err)
	}
	plan, err := codexgen.RunGlobalAgents(codexgen.GlobalAgentsOptions{
		Home:             installer.options.Home,
		SourceRepo:       sourceRepo,
		ClaudeConfigDirs: []string{installer.options.ConfigDir},
		CodexHomes:       installer.codexHomes(),
		Mode:             codexgen.ModeCheck,
	})
	if err != nil {
		return fmt.Errorf("plan pfm codex agents: %w", err)
	}
	for _, action := range plan.Actions {
		message := action.Kind + " " + action.Path
		if action.Target != "" {
			message += " -> " + action.Target
		}
		if err := installer.change(message, nil); err != nil {
			return err
		}
	}
	for _, problem := range plan.Problems {
		installer.skip(problem)
	}
	// Before the early dry-run return, so the preview IS the apply's plan:
	// installer.retire only removes when this run applies.
	if err := installer.retireOrphanCodexRoles(sourceRepo, plan.Roles); err != nil {
		return err
	}
	if err := installer.retireOrphanGlobalAgents(plan.Installed); err != nil {
		return err
	}
	if !installer.apply {
		return nil
	}
	result, err := codexgen.RunGlobalAgents(codexgen.GlobalAgentsOptions{
		Home:             installer.options.Home,
		SourceRepo:       sourceRepo,
		ClaudeConfigDirs: []string{installer.options.ConfigDir},
		CodexHomes:       installer.codexHomes(),
		Mode:             codexgen.ModeBuild,
	})
	if err != nil {
		return fmt.Errorf("run pfm codex agents: %w", err)
	}
	installer.ok(fmt.Sprintf("Codex global agents compiled=%d linked=%d", len(result.Compiled), len(result.Installed)))
	return nil
}

// globalSourceRepoRoot resolves the clone every global agent/command/skill
// symlink target and conflict check is anchored on: this run's explicit
// --source-repo when set, else the marker a prior install recorded, else the
// documented default clone location (INSTALL.md's $HOME/.professor) so a
// first-ever install with neither still resolves to something concrete
// rather than refusing to plan at all.
func (installer *engine) globalSourceRepoRoot() (string, error) {
	if repo := strings.TrimSpace(installer.options.SourceRepo); repo != "" {
		abs, err := filepath.Abs(repo)
		if err != nil {
			return "", fmt.Errorf("resolve source repository %q: %w", repo, err)
		}
		return filepath.Clean(abs), nil
	}
	return GlobalSourceRepo(installer.options.Home)
}

// retireOrphanCodexAgents deletes the exactly two known strays a retired
// "explorer" Codex agent left behind: its compiled TOML and any timestamped
// backup an earlier copy-based install wrote before this installer learned
// to symlink instead. Nothing else here is ever deleted.
func (installer *engine) retireOrphanCodexAgents() error {
	target := filepath.Join(installer.options.Home, ".codex", "agents", "explorer.toml")
	if err := installer.retire(target, "retired explorer agent"); err != nil {
		return err
	}
	matches, err := filepath.Glob(target + ".bak-*")
	if err != nil {
		return fmt.Errorf("glob retired explorer agent backups: %w", err)
	}
	for _, match := range matches {
		if err := installer.retire(match, "retired explorer agent backup"); err != nil {
			return err
		}
	}
	return nil
}

// wireGlobalCommands links each top-level clone command into the store commands registry.
// Dead links are pruned separately by retireDeadRegistryLinks.
func (installer *engine) wireGlobalCommands() error {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve global commands source repository: %w", err)
	}
	source := filepath.Join(sourceRepo, "templates", "global", "commands")
	entries, err := os.ReadDir(source)
	if errors.Is(err, fs.ErrNotExist) {
		installer.skip("global commands source absent at " + source + " (0 entries)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect global commands source %s: %w", source, err)
	}
	if len(entries) == 0 {
		installer.skip("global commands source empty at " + source + " (0 entries)")
		return nil
	}
	config := installer.options.ConfigDir
	target := filepath.Join(config, "commands")
	for _, entry := range entries {
		if err := installer.wireGlobalLink(
			filepath.Join(source, entry.Name()),
			filepath.Join(target, entry.Name()),
			sourceRepo,
			entry.IsDir(),
		); err != nil {
			return err
		}
	}

	return nil
}

// wireGlobalLink classifies target against source with codexgen's shared
// global-link machinery and reports/fixes it through this installer's own
// change/ok/skip transcript — the same ensureLink pattern (correct link is a
// no-op, anything else routed through installer.change so apply/dry-run
// agree), extended with the one rule ensureLink never needed: a target that
// is neither ours nor a link into the source repo is a conflict this
// installer refuses to touch.
func (installer *engine) wireGlobalLink(source, target, sourceRepoRoot string, isDir bool) error {
	kind := codexgen.GlobalLinkFile
	if isDir {
		kind = codexgen.GlobalLinkDir
	}
	state, found, err := codexgen.ClassifyGlobalLink(target, source, sourceRepoRoot, kind)
	if err != nil {
		return fmt.Errorf("inspect global link %s: %w", target, err)
	}
	if state == codexgen.GlobalLinkCorrect {
		installer.ok(target)
		return nil
	}
	message := codexgen.DescribeGlobalLinkState(state, target, source, found)
	if state == codexgen.GlobalLinkConflict {
		installer.skip(message)
		return nil
	}
	return installer.change(message, func() error {
		return codexgen.ApplyGlobalLink(target, source, state)
	})
}

func (installer *engine) reconcileCodexCommands(assets []assetFile) error {
	plan, err := installer.planCodexCommands(assets, !installer.apply)
	if err != nil {
		return err
	}
	for _, action := range plan.Actions {
		message := action.Kind + " " + action.Path
		if action.Target != "" {
			message += " -> " + action.Target
		}
		if err := installer.change(message, nil); err != nil {
			return err
		}
	}
	if blockers := codexPlanBlockers(plan.Problems); len(blockers) != 0 {
		for _, blocker := range blockers {
			installer.say("  conflict %s", blocker)
		}
		conflictErr := fmt.Errorf("reconcile Codex global commands: %s", strings.Join(blockers, "; "))
		if !installer.apply {
			installer.planErrors = append(installer.planErrors, conflictErr)
			return nil
		}
		return conflictErr
	}
	if !installer.apply {
		return nil
	}
	sourceHome := ""
	if installer.options.Mode == ModeUninstall {
		// Uninstall must never WRITE a fresh Codex command mirror entry — only
		// retire the ones this installer already owns. RunGlobalCommands derives
		// its "wanted" set by scanning SourceHome/.claude/commands; pointing it
		// at a fresh, guaranteed-empty directory makes that set unconditionally
		// empty, so the ModeBuild call below can only ever delete managed
		// entries, never create one for whatever still happens to sit in
		// ~/.claude/commands at this point in the teardown — one of this
		// installer's own not-yet-unwired commands, or a user's own foreign one
		// sharing the directory.
		empty, err := os.MkdirTemp("", "pfm-uninstall-codex-commands-")
		if err != nil {
			return fmt.Errorf("create empty Codex command source for uninstall: %w", err)
		}
		defer func() {
			if removeErr := os.RemoveAll(empty); removeErr != nil {
				installer.say("warning: remove temporary uninstall command source %s: %v", empty, removeErr)
			}
		}()
		sourceHome = empty
	}
	result, err := codexgen.RunGlobalCommands(codexgen.GlobalCommandsOptions{
		Home:       installer.options.Home,
		SourceHome: sourceHome,
		Mode:       codexgen.ModeBuild,
	})
	if err != nil {
		return fmt.Errorf("reconcile Codex global commands: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("reconcile Codex global commands: %s", strings.Join(result.Problems, "; "))
	}
	installer.ok(fmt.Sprintf(
		"Codex global commands wrote=%d unchanged=%d deleted=%d",
		result.Wrote,
		result.Unchanged,
		result.Deleted,
	))
	return nil
}

func codexPlanBlockers(problems []string) []string {
	blockers := make([]string, 0)
	for _, problem := range problems {
		if strings.HasPrefix(problem, "MISSING ") || strings.HasPrefix(problem, "STALE ") ||
			strings.HasPrefix(problem, "ORPHAN ") {
			continue
		}
		blockers = append(blockers, problem)
	}
	return blockers
}

func (installer *engine) planCodexCommands(assets []assetFile, future bool) (result codexgen.Result, returnErr error) {
	sourceHome := ""
	cleanup := func() error { return nil }
	if future && filepath.Clean(installer.options.ConfigDir) ==
		filepath.Join(filepath.Clean(installer.options.Home), ".claude") {
		var err error
		sourceHome, cleanup, err = installer.futureCommandSource(assets)
		if err != nil {
			return codexgen.Result{}, err
		}
	}
	defer func() {
		if err := cleanup(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	result, err := codexgen.RunGlobalCommands(codexgen.GlobalCommandsOptions{
		Home: installer.options.Home, SourceHome: sourceHome, Mode: codexgen.ModeCheck,
	})
	if err != nil {
		return codexgen.Result{}, fmt.Errorf("plan Codex global commands: %w", err)
	}
	return result, nil
}

func (installer *engine) futureCommandSource(assets []assetFile) (string, func() error, error) {
	temporary, err := os.MkdirTemp("", "pfm-install-command-plan-")
	if err != nil {
		return "", func() error { return nil }, fmt.Errorf("create Codex command plan root: %w", err)
	}
	cleanup := func() error {
		if err := os.RemoveAll(temporary); err != nil {
			return fmt.Errorf("remove Codex command plan root %s: %w", temporary, err)
		}
		return nil
	}
	fail := func(cause error) (string, func() error, error) {
		return "", func() error { return nil }, errors.Join(cause, cleanup())
	}
	source := filepath.Join(installer.options.Home, ".claude", "commands")
	target := filepath.Join(temporary, ".claude", "commands")
	if _, err := os.Lstat(source); err == nil {
		if err := copyCommandPlanTree(source, target); err != nil {
			return fail(fmt.Errorf("stage current Claude commands for preview: %w", err))
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fail(fmt.Errorf("inspect Claude commands for preview: %w", err))
	}
	for _, asset := range assets {
		commandTarget, found := installer.commandTarget(asset.path)
		if !found {
			continue
		}
		relative, err := filepath.Rel(filepath.Join(installer.options.ConfigDir, "commands"), commandTarget)
		if err != nil {
			return fail(fmt.Errorf("map planned command %s: %w", commandTarget, err))
		}
		content, err := readAsset(asset.path)
		if err != nil {
			return fail(fmt.Errorf("read planned command asset %s: %w", asset.path, err))
		}
		if err := atomicfile.Write(filepath.Join(target, relative), content, asset.mode); err != nil {
			return fail(fmt.Errorf("stage planned command %s: %w", relative, err))
		}
	}
	for _, retired := range []string{"bb.md", "swap.md"} {
		if err := os.RemoveAll(filepath.Join(target, retired)); err != nil {
			return fail(fmt.Errorf("retire %s from command preview: %w", retired, err))
		}
	}
	return temporary, cleanup, nil
}

// copyCommandPlanTree snapshots the current command source while omitting the
// two retired top-level commands. In particular, an old dangling /bb symlink
// must not make preview fail before the installer can report and retire it.
func copyCommandPlanTree(source, target string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "bb.md" || entry.Name() == "swap.md" {
			continue
		}
		if err := copyPlanTree(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyPlanTree(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			// A dangling link is exactly what this install is about to prune, so
			// the preview must skip it rather than abort before the prune runs.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		return copyPlanTree(resolved, target)
	}
	if info.IsDir() {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPlanTree(
				filepath.Join(source, entry.Name()),
				filepath.Join(target, entry.Name()),
			); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported command source mode %s", info.Mode())
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return atomicfile.Write(target, content, info.Mode().Perm())
}

func (installer *engine) uninstall(ctx context.Context) error {
	if err := installer.uninstallHarvest(); err != nil {
		return err
	}
	installer.uninstallThemes()
	if err := installer.removeRumdlUserConfig(); err != nil {
		return err
	}
	assets, err := assetFiles()
	if err != nil {
		return fmt.Errorf("enumerate embedded install assets: %w", err)
	}
	if err := installer.unwireClaudeLauncher(); err != nil {
		return err
	}
	if err := installer.unwireHostOverlays(); err != nil {
		return err
	}
	if err := installer.unwireCommands(assets); err != nil {
		return err
	}
	if err := installer.unwireSkills(assets); err != nil {
		return err
	}
	// Before the Codex mirror is reconciled: that step's wanted set is
	// derived from the Claude command registries this call just emptied, so
	// unwiring first is what lets the mirror drop the same globals instead of
	// recompiling them for an install that is going away.
	if err := installer.unwireGlobalRegistries(); err != nil {
		return err
	}
	if err := installer.retireDeadRegistryLinks(); err != nil {
		return err
	}
	if err := installer.reconcileCodexCommands(nil); err != nil {
		return err
	}
	if err := installer.unwireGeneratedCodexAgents(); err != nil {
		return err
	}
	if err := installer.retireBBInstall(); err != nil {
		return err
	}
	if err := installer.retireChatCommands(); err != nil {
		return err
	}
	if schedulerIsLaunchd {
		if err := installer.unwireLaunchAgent(ctx); err != nil {
			return err
		}
		if err := installer.unwireReminderLaunchAgent(ctx); err != nil {
			return err
		}
	}
	managerAvailable := installer.userManagerAvailable(ctx)
	if managerAvailable && installer.apply {
		installer.runSystemctl(
			ctx, "disable", "--now", nameSyncPathUnit, nameSyncTimerUnit, reminderTimerUnit, mcpUnitName,
		)
	}
	if _, err := installer.retireUnitEnablements(
		filepath.Join(installer.options.Home, ".config", "systemd", "user"),
		retiredUnitNames,
	); err != nil {
		return err
	}
	for _, enablement := range unitEnablements {
		target := filepath.Join(
			installer.options.Home,
			".config",
			"systemd",
			"user",
			enablement.wants,
			enablement.unit,
		)
		if err := installer.unlinkOne(target); err != nil {
			return err
		}
	}
	for _, name := range append(append([]string(nil), unitNames...), mcpUnitName) {
		if err := installer.unlinkOne(
			filepath.Join(installer.options.Home, ".config", "systemd", "user", name),
		); err != nil {
			return err
		}
	}
	if managerAvailable && installer.apply {
		installer.runSystemctl(ctx, "daemon-reload")
	}
	installer.deferFailure(installer.wireCodexHooks())
	if err := installer.wireMCP(); err != nil {
		return err
	}
	if err := installer.wireOpenCodeInstructions(); err != nil {
		return err
	}
	if err := installer.removeCodexDeveloperInstructions(); err != nil {
		return err
	}
	if err := installer.wireShell(true); err != nil {
		return err
	}
	installer.deferFailure(installer.wireLoginDefault(true))
	if err := installer.wireVSCode(); err != nil {
		return err
	}
	if err := installer.retireStagedManagedSurfaces(true); err != nil {
		return err
	}
	if err := installer.removeManagedAssets(assets); err != nil {
		return err
	}
	if installer.apply {
		if err := installer.removeUpdateMetadata(); err != nil {
			return err
		}
	}
	return nil
}

func harvestPythonRoot(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "harvest-python")
}

func (installer *engine) harvestPlatform() harvestpy.Platform {
	platform := installer.options.HarvestPlatform
	if platform.GOOS == "" {
		platform.GOOS, platform.GOARCH = goRuntime.GOOS, goRuntime.GOARCH
	}
	return platform
}

func (installer *engine) installHarvest(ctx context.Context) error {
	if !installer.options.ProvisionHarvest {
		if installer.apply {
			installer.say("harvestpy: skipped (blocked, not attempted)")
		} else {
			installer.say("harvestpy: would skip (blocked, not attempted)")
		}
		return nil
	}
	provider := installer.options.HarvestProvisioner
	if provider == nil {
		return errors.New("harvestpy provisioning is enabled but no provisioner is configured")
	}
	platform := installer.harvestPlatform()
	plan, err := provider.Plan(platform)
	if err != nil {
		return fmt.Errorf("harvestpy install plan for %s: %w", platform, err)
	}
	installer.say(
		"harvestpy plan: platform=%s Python %s uv=%s uv_download_bytes=%d python_download_bytes=%d package_download_bytes=%d environment_bytes=%d package_status=%s offline=%t",
		plan.Platform,
		plan.PythonVersion,
		plan.UVVersion,
		plan.UVDownloadBytes,
		plan.PythonDownloadBytes,
		plan.PackageDownloadBytes,
		plan.EnvironmentBytes,
		plan.PackageDownloadStatus,
		installer.options.HarvestOffline,
	)
	if len(plan.PackageBlockers) != 0 || plan.PackageDownloadStatus == "blocked-exact-lock" {
		installer.say("harvestpy plan: BLOCKED exact lock for %s", platform)
		for _, blocker := range plan.PackageBlockers {
			installer.say("  blocker %s", blocker)
		}
		return fmt.Errorf("harvestpy target %s is blocked-exact-lock", platform)
	}
	if !installer.apply {
		installer.say(
			"harvestpy dry-run: Plan only; writes=0 network=0 offline=%t (apply requires valid cached pins or network)",
			installer.options.HarvestOffline,
		)
		return nil
	}
	root := harvestPythonRoot(installer.options.Home)
	check, checkErr := provider.Check(ctx, root, platform)
	if checkErr == nil && check.Healthy {
		installer.ok("harvestpy environment already healthy (Check fast-path; no download)")
		return installer.stageHarvestModels(ctx, provider, root, platform)
	}
	if checkErr != nil {
		installer.say("harvestpy Check did not establish a healthy environment; provisioning: %v", checkErr)
	} else {
		installer.say("harvestpy Check reported an unhealthy environment; provisioning")
	}
	result, provisionErr := provider.Provision(ctx, harvestpy.ProvisionOptions{
		Root:     root,
		Cache:    filepath.Join(root, "cache"),
		Platform: platform,
		Offline:  installer.options.HarvestOffline,
	})
	if provisionErr != nil {
		if errors.Is(provisionErr, harvestpy.ErrOfflineUnavailable) {
			installer.say(
				"harvestpy offline: required pinned input is not in the verified cached inputs; prewarm the cache or rerun with network",
			)
			return fmt.Errorf(
				"harvestpy provisioning offline; verified cached inputs are unavailable: %w",
				provisionErr,
			)
		}
		return fmt.Errorf("harvestpy provision %s: %w", platform, provisionErr)
	}
	installer.ok("harvestpy environment provisioned digest=" + result.Digest)
	return installer.stageHarvestModels(ctx, provider, root, platform)
}

func (installer *engine) uninstallHarvest() error {
	if !installer.options.ProvisionHarvest {
		return nil
	}
	root := harvestPythonRoot(installer.options.Home)
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		installer.ok("managed harvestpy runtime and cache absent")
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect managed harvestpy root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refuse to remove managed harvestpy root that is not a directory: %s", root)
	}
	hasManaged := false
	for _, name := range []string{harvestEnvDirName, harvestCacheDirName} {
		if _, statErr := os.Lstat(filepath.Join(root, name)); statErr == nil {
			hasManaged = true
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("inspect managed harvestpy %s: %w", name, statErr)
		}
	}
	if !hasManaged {
		installer.ok("managed harvestpy runtime and cache absent")
		return nil
	}
	return installer.change("remove managed harvestpy runtime and cache", func() error {
		for _, name := range []string{harvestEnvDirName, harvestCacheDirName} {
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				return fmt.Errorf("remove managed harvestpy %s: %w", name, err)
			}
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return fmt.Errorf("inspect managed harvestpy root after removal: %w", err)
		}
		if len(entries) == 0 {
			if err := os.Remove(root); err != nil {
				return fmt.Errorf("remove empty managed harvestpy root: %w", err)
			}
		}
		return nil
	})
}

func (installer *engine) say(format string, arguments ...any) {
	if installer.outputErr != nil {
		return
	}
	_, installer.outputErr = fmt.Fprintf(installer.options.Stdout, format+"\n", arguments...)
}

func (installer *engine) change(message string, action func() error) error {
	installer.say("  change  %s", message)
	installer.report.Changed++
	if !installer.apply || action == nil {
		installer.record("change", message, nil)
		return nil
	}
	err := action()
	installer.record("change", message, err)
	return err
}

// changeDescription selects the message installer.change reports for a
// conditional rewrite: "create" when nothing existed to back up, and
// "rewrite ... (backup preserved)" only when a backup will actually be
// written. Callers must pass the SAME existed value that gates the backup —
// deriving it a second, independent way is how the message and the write it
// describes drift apart.
func changeDescription(path string, existed bool) string {
	if !existed {
		return "create " + path
	}
	return "rewrite " + path + " (backup preserved)"
}

func (installer *engine) ok(message string) {
	installer.say("  ok      %s", message)
	installer.report.OK++
	installer.record("ok", message, nil)
}

func (installer *engine) skip(message string) {
	installer.say("  skip    %s", message)
	installer.report.Skipped++
	installer.record("skip", message, nil)
}

func (installer *engine) stageAssets(assets []assetFile) (bool, error) {
	installer.say("embedded assets -> %s", installer.managedRoot)
	systemdChanged := false
	for _, asset := range assets {
		if mcpSchedulerAsset(asset.path) && !installer.mcpAnyEnabled() {
			continue
		}
		content, err := readAsset(asset.path)
		if err != nil {
			return false, fmt.Errorf("read embedded asset %s: %w", asset.path, err)
		}
		switch {
		case asset.path == "reload.command.md":
			content, err = renderReloadCommandAsset(content)
		case asset.path == "systemd/"+nameSyncTimerUnit:
			content, err = renderNameSyncTimerAsset(content, installer.options)
		case strings.HasPrefix(asset.path, "systemd/"):
			content, err = renderServicePath(content, installer.options.Home)
		}
		if err != nil {
			return false, fmt.Errorf("render embedded asset %s: %w", asset.path, err)
		}
		target := filepath.Join(installer.managedRoot, filepath.FromSlash(asset.path))
		if sameFile(target, content, asset.mode) {
			installer.ok(target)
			continue
		}
		if strings.HasPrefix(asset.path, "systemd/") {
			systemdChanged = true
		}
		if err := installer.change("write "+target, func() error {
			return atomicfile.Write(target, content, asset.mode)
		}); err != nil {
			return false, err
		}
	}
	if !installer.mcpAnyEnabled() {
		for _, relative := range []string{"systemd/pfm-mcp.service", "launchd/com.professor.pfm.mcp.plist"} {
			mcpAsset := filepath.Join(installer.managedRoot, filepath.FromSlash(relative))
			if _, err := os.Lstat(mcpAsset); err == nil {
				message := fmt.Sprintf(
					"remove %s (no MCP server is enabled in %s)",
					mcpAsset,
					installer.options.MCPConfigPath,
				)
				if err := installer.change(message, func() error {
					return os.Remove(mcpAsset)
				}); err != nil {
					return false, err
				}
				systemdChanged = systemdChanged || strings.HasPrefix(relative, "systemd/")
			} else if !errors.Is(err, fs.ErrNotExist) {
				return false, err
			}
		}
	}
	installer.say("")
	return systemdChanged, nil
}

func (installer *engine) removeManagedAssets(assets []assetFile) error {
	installer.say("managed assets -> %s", installer.managedRoot)
	for _, asset := range assets {
		target := filepath.Join(installer.managedRoot, filepath.FromSlash(asset.path))
		if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := installer.change("remove "+target, func() error { return os.Remove(target) }); err != nil {
			return err
		}
	}
	if installer.apply {
		directories := make(map[string]bool)
		for _, asset := range assets {
			for directory := filepath.Dir(filepath.Join(installer.managedRoot, filepath.FromSlash(asset.path))); strings.HasPrefix(directory, installer.managedRoot); directory = filepath.Dir(directory) {
				if directory == installer.managedRoot {
					break
				}
				directories[directory] = true
			}
		}
		ordered := make([]string, 0, len(directories))
		for directory := range directories {
			ordered = append(ordered, directory)
		}
		sort.Slice(ordered, func(left, right int) bool {
			return strings.Count(ordered[left], string(filepath.Separator)) >
				strings.Count(ordered[right], string(filepath.Separator))
		})
		for _, directory := range ordered {
			if err := os.Remove(
				directory,
			); err != nil && !errors.Is(err, fs.ErrNotExist) &&
				!errors.Is(err, fs.ErrInvalid) {
				installer.skip("leave non-empty managed directory " + directory + ": " + err.Error())
			}
		}
	}
	return nil
}

func (installer *engine) ensureLink(source, target string) (bool, error) {
	if current, linked := resolvedLink(target); linked && current == filepath.Clean(source) {
		installer.ok(target)
		return false, nil
	}
	info, err := os.Lstat(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	description := "link " + target + " -> " + source
	backup := ""
	if info != nil && info.Mode()&os.ModeSymlink == 0 {
		backup = availableBackup(target, installer.stamp)
	}
	return true, installer.change(description, func() error {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if info != nil {
			if backup == "" {
				if err := os.Remove(target); err != nil {
					return err
				}
			} else {
				if err := os.Rename(target, backup); err != nil {
					return fmt.Errorf("backup %s to %s: %w", target, backup, err)
				}
			}
		}
		return os.Symlink(source, target)
	})
}

func (installer *engine) unlinkOne(target string) error {
	_, linked := resolvedLink(target)
	if !linked {
		installer.skip(target + " is not an installed link")
		return nil
	}
	backup := newestBackup(target)
	if backup == "" {
		return installer.change("remove "+target, func() error { return os.Remove(target) })
	}
	message := "restore " + target + " from " + filepath.Base(backup)
	return installer.change(message, func() error {
		if err := os.Remove(target); err != nil {
			return err
		}
		return os.Rename(backup, target)
	})
}

func (installer *engine) retire(path, reason string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refuse to retire directory %s", path)
	}
	installer.markRemoved(path)
	return installer.change("retire "+path+" ("+reason+")", func() error {
		return os.Remove(path)
	})
}

func (installer *engine) retireGlob(pattern, reason string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, match := range matches {
		if err := installer.retire(match, reason); err != nil {
			return err
		}
	}
	return nil
}

// hostOverlayScripts names the assets/bin/* scripts pfm install owns end to
// end as HOST overlays — materialized like every other embedded asset by
// stageAssets, then symlinked at their contracted ~/.local/bin/NAME so a
// human or a cron unit can invoke them by that one name:
//   - pfm-statusline: the context-gauge overlay over `pfm statusline`.
//   - tmux-title-renudge: the OSC-title re-emitter the pfm-name-sync
//     systemd/launchd trio fires on a timer.
//
// Unlike the Claude launcher, an overlay has no displaced native binary to
// record and restore — the previous destination (a wrong symlink or a stale
// regular file) is simply superseded, the same idempotent replace-or-leave
// ensureLink already gives every command and skill link.
var hostOverlayScripts = []string{"pfm-statusline", "tmux-title-renudge"}

func managedHostOverlay(home, name string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install", "bin", name)
}

func canonicalHostOverlay(home, name string) string {
	return filepath.Join(home, ".local", "bin", name)
}

// HostOverlayState mirrors LauncherState for the two host-overlay scripts.
type HostOverlayState string

const (
	HostOverlayOK        HostOverlayState = "ok"
	HostOverlayMissing   HostOverlayState = "missing"
	HostOverlayDisplaced HostOverlayState = "displaced"
)

// HostOverlayStatus is one contracted overlay's canonical-symlink state.
type HostOverlayStatus struct {
	Name   string
	State  HostOverlayState
	Target string
}

// InspectHostOverlays reports the canonical ~/.local/bin/NAME symlink state
// of every installer-owned host overlay script, in the OK/missing/displaced
// vocabulary inspectClaudeLauncher uses: an absent link is missing; a link
// (or a non-symlink file) that does not resolve to the managed copy is
// displaced; only a link that resolves to exactly the managed copy is ok.
func InspectHostOverlays(home string) []HostOverlayStatus {
	statuses := make([]HostOverlayStatus, 0, len(hostOverlayScripts))
	for _, name := range hostOverlayScripts {
		canonical := canonicalHostOverlay(home, name)
		managed := managedHostOverlay(home, name)
		target, linked := resolvedLink(canonical)
		switch {
		case !linked:
			state := HostOverlayMissing
			if _, err := os.Lstat(canonical); err == nil {
				state = HostOverlayDisplaced
			}
			statuses = append(statuses, HostOverlayStatus{Name: name, State: state, Target: canonical})
		case target != filepath.Clean(managed):
			statuses = append(statuses, HostOverlayStatus{Name: name, State: HostOverlayDisplaced, Target: target})
		default:
			statuses = append(statuses, HostOverlayStatus{Name: name, State: HostOverlayOK, Target: target})
		}
	}
	return statuses
}

// wireHostOverlays links the two contracted ~/.local/bin overlay scripts to
// their managed copies. stageAssets already wrote the managed copies
// themselves (they are ordinary embedded assets); this is the one step
// stageAssets does not do on its own, same division of labor as
// wireClaudeLauncher over the managed Claude binary.
func (installer *engine) wireHostOverlays() error {
	installer.say("host overlays -> %s", filepath.Join(installer.options.Home, ".local", "bin"))
	for _, name := range hostOverlayScripts {
		if _, err := installer.ensureLink(
			managedHostOverlay(installer.options.Home, name),
			canonicalHostOverlay(installer.options.Home, name),
		); err != nil {
			return err
		}
	}
	installer.say("")
	return nil
}

func (installer *engine) unwireHostOverlays() error {
	installer.say("host overlays -> %s", filepath.Join(installer.options.Home, ".local", "bin"))
	for _, name := range hostOverlayScripts {
		if err := installer.unlinkOne(canonicalHostOverlay(installer.options.Home, name)); err != nil {
			return err
		}
	}
	installer.say("")
	return nil
}

// wireCommands links pfm commands into the store commands registry.
func (installer *engine) wireCommands(assets []assetFile) error {
	config := installer.options.ConfigDir
	installer.say("commands -> %s", filepath.Join(config, "commands"))
	for _, asset := range assets {
		target, found := installer.commandTargetIn(config, asset.path)
		if !found {
			continue
		}
		source := filepath.Join(installer.managedRoot, filepath.FromSlash(asset.path))
		if _, err := installer.ensureLink(source, target); err != nil {
			return err
		}
	}

	installer.say("")
	return nil
}

func (installer *engine) unwireCommands(assets []assetFile) error {
	config := installer.options.ConfigDir
	installer.say("commands -> %s", filepath.Join(config, "commands"))
	for _, asset := range assets {
		target, found := installer.commandTargetIn(config, asset.path)
		if !found {
			continue
		}
		if err := installer.unlinkOne(target); err != nil {
			return err
		}
	}

	if err := installer.retireLegacySwapCommand(); err != nil {
		return err
	}
	installer.say("")
	return nil
}

func (installer *engine) commandTarget(asset string) (string, bool) {
	return installer.commandTargetIn(installer.options.ConfigDir, asset)
}

func (installer *engine) commandTargetIn(config, asset string) (string, bool) {
	commands := filepath.Join(config, "commands")
	if asset == "reload.command.md" {
		return filepath.Join(commands, "reload.md"), true
	}
	return "", false
}

func (installer *engine) wireSkills(assets []assetFile) error {
	installer.say("skills -> %s", filepath.Join(installer.options.ConfigDir, "skills"))
	config := installer.options.ConfigDir
	for _, asset := range assets {
		target, found := installer.skillTarget(config, asset.path)
		if !found {
			continue
		}
		source := filepath.Join(installer.managedRoot, filepath.FromSlash(asset.path))
		if _, err := installer.ensureLink(source, target); err != nil {
			return err
		}
	}

	installer.say("")
	return nil
}

func (installer *engine) unwireSkills(assets []assetFile) error {
	installer.say("skills -> %s", filepath.Join(installer.options.ConfigDir, "skills"))
	config := installer.options.ConfigDir
	for _, asset := range assets {
		target, found := installer.skillTarget(config, asset.path)
		if !found {
			continue
		}
		if err := installer.unlinkOne(target); err != nil {
			return err
		}
		// The skill's own directory (e.g. skills/handoff/) is created by
		// ensureLink's MkdirAll on link; remove it here once its one link is
		// gone, tolerantly — an operator file left beside it must survive.
		if err := installer.retireEmptyDirTolerant(filepath.Dir(target)); err != nil {
			return err
		}
	}

	installer.say("")
	return nil
}

// skillTarget names an embedded skill asset in the store skills registry.
func (installer *engine) skillTarget(configDir, asset string) (string, bool) {
	skills := filepath.Join(configDir, "skills")
	if asset == "handoff.skill.md" {
		return filepath.Join(skills, "handoff", "SKILL.md"), true
	}
	return "", false
}

func (installer *engine) retireLegacySwapCommand() error {
	path := filepath.Join(installer.options.ConfigDir, "commands", "swap.md")
	if target, linked := resolvedLink(
		path,
	); linked &&
		target == filepath.Join(installer.managedRoot, "swap.command.md") {
		if err := installer.unlinkOne(path); err != nil {
			return err
		}
	}
	return installer.retire(filepath.Join(installer.managedRoot, "swap.command.md"), "retired swap command")
}

func (installer *engine) recordedProfessorSourceRepos() ([]string, error) {
	repos := make([]string, 0, 2)
	seen := map[string]bool{}
	add := func(repo string) {
		repo = paths.PhysicalPath(filepath.Clean(strings.TrimSpace(repo)))
		if repo != "." && !seen[repo] {
			seen[repo] = true
			repos = append(repos, repo)
		}
	}
	add(installer.options.SourceRepo)
	marker := paths.SourceRepoPath(installer.options.Home)
	if _, err := os.Lstat(marker); errors.Is(err, fs.ErrNotExist) {
		return repos, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect source repository marker for registry dead-link check: %w", err)
	}
	repo, err := paths.ReadSourceRepoMarker(installer.options.Home)
	if err != nil {
		return nil, fmt.Errorf("read source repository marker for registry dead-link check: %w", err)
	}
	add(repo)
	return repos, nil
}

// retireEmptyDirTolerant removes path only once it has actually turned out
// empty. Unlike retireEmptyDir, a directory left non-empty is a
// routine, visible skip rather than a hard failure — the same tolerant
// os.Remove-and-skip idiom removeManagedAssets uses for its own directory
// sweep, appropriate wherever content the operator (not this installer)
// controls can legitimately remain.
func (installer *engine) retireEmptyDirTolerant(path string) error {
	if !installer.apply {
		return nil
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		installer.skip("leave non-empty directory " + path + ": " + err.Error())
		return nil
	}
	installer.ok("removed empty " + path)
	return nil
}

var unitNames = []string{
	nameSyncPathUnit,
	"pfm-name-sync.service",
	nameSyncTimerUnit,
	reminderServiceUnit,
	reminderTimerUnit,
}

const (
	mcpUnitName         = "pfm-mcp.service"
	harvestEnvDirName   = "env"
	harvestCacheDirName = "cache"
	systemdDefaultWants = "default.target.wants"
)

var retiredUnitNames = []string{
	"cc-fleet-name-sync.path", "cc-fleet-name-sync.service", "cc-fleet-name-sync.timer",
	"cc-name-sync.path", "cc-name-sync.service", "cc-name-sync.timer",
}

var unitEnablements = []struct {
	unit  string
	wants string
}{
	{unit: nameSyncPathUnit, wants: systemdDefaultWants},
	{unit: nameSyncTimerUnit, wants: "timers.target.wants"},
	{unit: reminderTimerUnit, wants: "timers.target.wants"},
	{unit: mcpUnitName, wants: systemdDefaultWants},
}

func (installer *engine) wireUnits(ctx context.Context) (bool, error) {
	directory := filepath.Join(installer.options.Home, ".config", "systemd", "user")
	installer.say("systemd user units -> %s", directory)
	managerAvailable := installer.userManagerAvailable(ctx)
	changed := false
	enablementChanged, err := installer.retireUnitEnablements(directory, retiredUnitNames)
	if err != nil {
		return false, err
	}
	changed = changed || enablementChanged
	for _, name := range retiredUnitNames {
		target := filepath.Join(directory, name)
		_, statErr := os.Lstat(target)
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return false, fmt.Errorf("inspect retired unit %s: %w", target, statErr)
		}
		known := statErr == nil
		if managerAvailable && !known {
			known = installer.unitKnown(ctx, name)
		}
		if !known {
			continue
		}
		if managerAvailable && installer.apply {
			installer.runSystemctl(ctx, "disable", "--now", name)
			installer.runSystemctl(ctx, "reset-failed", name)
		}
		if statErr == nil {
			if err := installer.retire(target, "superseded by pfm-name-sync"); err != nil {
				return false, err
			}
			changed = true
		}
	}
	managedNames := append([]string(nil), unitNames...)
	if installer.mcpAnyEnabled() {
		managedNames = append(managedNames, mcpUnitName)
	} else {
		mcpTarget := filepath.Join(directory, mcpUnitName)
		if _, err := os.Lstat(mcpTarget); err == nil {
			if managerAvailable && installer.apply {
				installer.runSystemctl(ctx, "disable", "--now", mcpUnitName)
			}
			if err := installer.unlinkOne(mcpTarget); err != nil {
				return false, err
			}
			changed = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	for _, name := range managedNames {
		source := filepath.Join(installer.managedRoot, "systemd", name)
		target := filepath.Join(directory, name)
		linkChanged, err := installer.ensureLink(source, target)
		if err != nil {
			return false, err
		}
		changed = changed || linkChanged
	}
	for _, enablement := range unitEnablements {
		source := filepath.Join(directory, enablement.unit)
		target := filepath.Join(directory, enablement.wants, enablement.unit)
		if enablement.unit == mcpUnitName && !installer.mcpAnyEnabled() {
			info, err := os.Lstat(target)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return false, err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				installer.skip("unexpected non-symlink MCP enablement left untouched: " + target)
				continue
			}
			if err := installer.change("remove "+target, func() error {
				return os.Remove(target)
			}); err != nil {
				return false, err
			}
			changed = true
			continue
		}
		linkChanged, err := installer.ensureLink(source, target)
		if err != nil {
			return false, err
		}
		changed = changed || linkChanged
	}
	if !managerAvailable {
		installer.skip("systemd --user unavailable; units are staged and enabled for next login but not started now")
	}
	installer.say("")
	return changed, nil
}

func (installer *engine) retireUnitEnablements(directory string, names []string) (bool, error) {
	changed := false
	for _, wants := range []string{systemdDefaultWants, "timers.target.wants"} {
		for _, name := range names {
			target := filepath.Join(directory, wants, name)
			info, err := os.Lstat(target)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return false, fmt.Errorf("inspect retired enablement %s: %w", target, err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				installer.skip("unexpected non-symlink retired enablement left untouched: " + target)
				continue
			}
			if err := installer.retire(target, "superseded systemd enablement"); err != nil {
				return false, err
			}
			changed = true
		}
	}
	return changed, nil
}

func (installer *engine) reloadUnits(ctx context.Context) {
	if !installer.apply || !installer.userManagerAvailable(ctx) {
		return
	}
	installer.runSystemctl(ctx, "daemon-reload")
	enablements := [][]string{{nameSyncPathUnit, nameSyncTimerUnit, reminderTimerUnit}}
	if installer.mcpAnyEnabled() {
		enablements = append(enablements, []string{mcpUnitName})
	}
	for _, units := range enablements {
		args := append([]string{"enable", "--now"}, units...)
		if err := userSystemctl(ctx, installer.options.Runner, args...); err != nil {
			installer.deferFailure(installer.fail(
				fmt.Errorf("systemctl --user enable --now %s: %w", strings.Join(units, " "), err),
			))
		}
	}
}

func (installer *engine) userManagerAvailable(ctx context.Context) bool {
	return installer.options.Runner.Run(ctx, "systemctl", "--user", "show-environment") == nil
}

// restartMCPUnit restarts pfm-mcp.service, waits the settle and verifies it
// came back active; a restart that fails or does not come back is an error,
// reported at once, never a skip.
func (installer *engine) restartMCPUnit(ctx context.Context) error {
	err := userSystemctl(ctx, installer.options.Runner, "restart", mcpUnitName)
	if err != nil {
		err = fmt.Errorf("systemctl --user restart %s: %w", mcpUnitName, err)
	} else {
		installer.pause(fleetUnitSettle)
		err = verifyFleetUnitsActive(ctx, installer.options.Runner, []string{mcpUnitName})
	}
	if err != nil {
		return installer.fail(err)
	}
	installer.ok("systemctl --user restart " + mcpUnitName)
	return nil
}

func (installer *engine) unitKnown(ctx context.Context, unit string) bool {
	for _, command := range []string{"is-active", "is-enabled", "is-failed"} {
		if installer.options.Runner.Run(ctx, "systemctl", "--user", command, "--quiet", unit) == nil {
			return true
		}
	}
	return false
}

func (installer *engine) runSystemctl(ctx context.Context, arguments ...string) {
	args := append([]string{"--user"}, arguments...)
	if err := installer.options.Runner.Run(ctx, "systemctl", args...); err != nil {
		installer.skip("systemctl " + strings.Join(args, " ") + " failed: " + err.Error())
	}
}

func (installer *engine) writeSettingsHookOwnership(
	path string,
	existing []byte,
	ownership map[string]settingsHookCounts,
) error {
	if len(ownership) == 0 {
		if len(existing) == 0 {
			installer.ok(path)
			return nil
		}
		return installer.change("remove "+path, func() error { return os.Remove(path) })
	}
	same, encoded, err := sameSettingsHookOwnership(existing, ownership)
	if err != nil {
		return err
	}
	if same && sameFile(path, encoded, 0o600) {
		installer.ok(path)
		return nil
	}
	return installer.change("write "+path, func() error {
		return atomicfile.Write(path, encoded, 0o600)
	})
}

func (installer *engine) codexHomes() []string {
	if installer.options.CodexHomes == nil {
		return []string{filepath.Join(installer.options.Home, ".codex")}
	}
	return installer.options.CodexHomes
}

func (installer *engine) wireShell(uninstall bool) error {
	zshrc := filepath.Join(installer.options.Home, ".zshrc")
	// The clone being installed wins: a first install records the marker
	// only later in this same run.
	repo := strings.TrimSpace(installer.options.SourceRepo)
	if !uninstall && repo == "" {
		var err error
		repo, err = paths.ReadSourceRepoMarker(installer.options.Home)
		if errors.Is(err, paths.ErrNoSourceRepoMarker) {
			repo, err = GlobalSourceRepo(installer.options.Home)
			if err != nil {
				installer.skip("zshrc: " + err.Error() + " — rerun pfm install --yes from inside your Professor clone")
				return nil
			}
			shim := filepath.Join(repo, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh")
			if _, err := os.Stat(shim); errors.Is(err, fs.ErrNotExist) {
				installer.skip("zshrc: no source repo recorded and " + shim + " does not exist")
				return nil
			} else if err != nil {
				return fmt.Errorf("inspect fallback shell shim %s: %w", shim, err)
			}
		} else if err != nil {
			installer.skip("zshrc: " + err.Error() + " — rerun pfm install --yes from inside your Professor clone")
			return nil
		}
	}
	shim := filepath.Join(repo, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh")
	wanted := sourceLine(shim)
	raw, err := os.ReadFile(zshrc)
	if errors.Is(err, fs.ErrNotExist) {
		raw = nil
	} else if err != nil {
		return err
	}
	if !uninstall {
		installer.reportEarlyFleetCalls(string(raw))
	}
	updated := rewriteZshrc(string(raw), wanted, uninstall)
	if string(raw) == updated {
		installer.ok(zshrc + " source line")
		return nil
	}
	description := "rewrite " + zshrc + " (backup preserved)"
	if len(raw) == 0 {
		description = "create " + zshrc
	}
	backup := ""
	if len(raw) > 0 {
		backup = availableBackup(zshrc, installer.stamp)
	}
	return installer.change(description, func() error {
		if len(raw) > 0 {
			if err := copyBackup(zshrc, backup); err != nil {
				return err
			}
		}
		if uninstall && updated == "" {
			return os.Remove(zshrc)
		}
		return atomicfile.Write(zshrc, []byte(updated), 0o600)
	})
}

// reportEarlyFleetCalls names a fleet command CALLED above the source line — a
// class that cannot announce itself, because `cc` and `cx` are names the system
// already owns. It only reports: a line of the operator's own shell that is not
// ours is never rewritten from here.
func (installer *engine) reportEarlyFleetCalls(content string) {
	early := earlyFleetCalls(content)
	if len(early) == 0 {
		installer.ok("nothing above the source line calls a fleet command")
		return
	}
	installer.skip("a fleet command runs ABOVE the source line, where it does not exist yet:")
	for _, line := range early {
		installer.say("          %s", line)
	}
	installer.say("          There, `cc` is /usr/bin/cc — the C compiler — not this fleet.")
	installer.say("          Replace retired cc* calls with pfm BELOW the source line. For a terminal profile that")
	installer.say("          opens the picker on launch, delete them instead and set PFM_AUTO_OPEN=pfm in")
	installer.say("          the profile's env: the shim runs that hook itself, last, once the")
	installer.say("          launchers are defined.")
}

func (installer *engine) migrateOldState() error {
	oldState := filepath.Join(installer.options.Home, ".local", "state", legacyFleetBinary)
	state := filepath.Join(installer.options.Home, ".local", "state", "pfm")
	oldInfo, oldErr := os.Stat(oldState)
	_, stateErr := os.Stat(state)
	if oldErr == nil && oldInfo.IsDir() && errors.Is(stateErr, fs.ErrNotExist) {
		return installer.change("migrate "+oldState+" -> "+state, func() error {
			if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
				return err
			}
			return os.Rename(oldState, state)
		})
	}
	if oldErr == nil && stateErr == nil {
		installer.skip("both old and pfm state directories exist; left both in place")
	}
	return nil
}

func (installer *engine) migrateLegacyCarrier(ctx context.Context) (returnErr error) {
	carrier := filepath.Join(installer.options.Home, ".claude", ".cc-ls-hidden")
	file, err := os.Open(carrier)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read retired kill carrier: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close retired kill carrier: %w", err))
		}
	}()
	seen := map[string]bool{}
	var ids []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		id := strings.TrimSpace(scanner.Text())
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan retired kill carrier: %w", err)
	}
	sort.Strings(ids)
	return installer.change(
		fmt.Sprintf("merge %d retired carrier kill(s) into SQLite without overwriting existing rows", len(ids)),
		func() (returnErr error) {
			values := paths.Values{
				Home:    installer.options.Home,
				StateDB: installer.options.StateDB,
			}
			state := fleetdb.OpenSharedState(ctx, values)
			defer func() {
				if err := state.Close(); err != nil {
					returnErr = errors.Join(
						returnErr,
						fmt.Errorf("close shared store after carrier retirement: %w", err),
					)
				}
			}()
			if err := state.Degraded(); err != nil {
				return fmt.Errorf("open shared store before carrier retirement: %w", err)
			}
			existing, err := state.KilledAt(ctx)
			if err != nil {
				return fmt.Errorf("read shared kills before carrier retirement: %w", err)
			}
			for _, id := range ids {
				if _, found := existing[id]; found {
					continue
				}
				if err := state.Kill(ctx, id, 0); err != nil {
					return fmt.Errorf("import retired carrier kill %q: %w", id, err)
				}
			}
			return nil
		},
	)
}

func (installer *engine) retirePredecessors() error {
	if err := installer.retireLegacyCommands(); err != nil {
		return err
	}
	config := installer.options.ConfigDir
	for _, name := range []string{
		"cx-kill.sh", "bb-hook.sh", "cx-heal.sh", "cx-recover.sh",
	} {
		if err := installer.retire(filepath.Join(config, "bin", name), "native pfm command"); err != nil {
			return err
		}
	}
	if err := installer.retireGlob(
		filepath.Join(config, "bin", "cx-recover.sh.pre-professor-*"),
		"retired recovery artifact",
	); err != nil {
		return err
	}
	for _, name := range []string{
		"statusline-command.sh",
		"statusline/segments.d/10-vertex-spend.sh",
		"statusline/segments.d/40-gpt-account.sh",
		"statusline/vertex-spend-refresh.py",
		"statusline/gpt-usage.py",
		"statusline/vertex_daily_tokens.py",
	} {
		if err := installer.retire(
			filepath.Join(config, filepath.FromSlash(name)),
			"native pfm statusline",
		); err != nil {
			return err
		}
	}
	for _, name := range []string{"dump.md", "chat-ops.sh", "group.sh"} {
		if err := installer.retire(
			filepath.Join(config, "commands", "chat", name),
			"native pfm chat command",
		); err != nil {
			return err
		}
	}

	// The per-engine harness-prompts tree replaced the flat staged prompts/
	// directory; a host installed before the move still carries its files.
	if err := installer.retireGlob(
		filepath.Join(installer.managedRoot, "prompts", "*"),
		"harness prompts moved to harness-prompts/",
	); err != nil {
		return err
	}
	if err := installer.retireEmptyDirTolerant(filepath.Join(installer.managedRoot, "prompts")); err != nil {
		return err
	}
	carrier := filepath.Join(installer.options.Home, ".claude", ".cc-ls-hidden")
	for _, path := range []string{carrier, carrier + ".at", carrier + ".lock"} {
		if err := installer.retire(path, "SQLite is the sole kill store"); err != nil {
			return err
		}
	}
	return installer.retireGlob(carrier+".n.*", "retired carrier scratch")
}

// Retire executable predecessors across the configured account roster while
// leaving account credentials, transcript directories, and live sockets alone.
func (installer *engine) retireLegacyCommands() error {
	for _, name := range []string{legacyFleetBinary, "cc-ls", "cc-open", "cc-swap", "cc-revive", "cc-clean"} {
		if err := installer.retireLegacyCommand(
			filepath.Join(installer.options.Home, ".local", "bin", name),
		); err != nil {
			return err
		}
	}
	configDirs := []string{installer.options.ConfigDir}
	for _, account := range installer.options.ClaudeAccounts {
		configDirs = append(configDirs, account.ConfigDir)
	}
	for _, configDir := range configDirs {
		if strings.TrimSpace(configDir) == "" {
			continue
		}
		for _, name := range []string{
			"cc-launch.sh", "cc-lib.sh", "cc-reseed.sh", "cc-account-swap.sh", "cc-account-swap-all.sh",
			"cc-ls.sh", "cc-open.sh", "cc-revive.sh", "cc-clean.sh", "cc-usage-hook.sh", "cc-fleet.zsh",
			"cc-kill.sh", "cc-archive.sh", "cc-reap.sh", "cc-name-sync.sh", "cc-portable.sh", "cc-db.sh",
			"cc-agent-open.sh", "cc-swap-chat.sh",
		} {
			if err := installer.retireLegacyCommand(filepath.Join(configDir, "bin", name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Retired command names are reserved, but their files may have been customized.
// Preserve regular files outside PATH before removing their old executable name.
func (installer *engine) retireLegacyCommand(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect retired command %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return installer.retire(path, "retired command link; target preserved")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refuse to retire non-regular command %s", path)
	}
	backup := availableBackup(
		filepath.Join(installer.options.Home, ".local", "state", "pfm", "retired-commands", filepath.Base(path)),
		installer.stamp,
	)
	return installer.change("retire "+path+" (backup: "+backup+")", func() error {
		if err := copyBackup(path, backup); err != nil {
			return fmt.Errorf("backup retired command %s: %w", path, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove retired command %s after backup %s: %w", path, backup, err)
		}
		return nil
	})
}

// retiredGlobalAgents names every global Claude agent identity a template
// rename has retired: the OLD name, left behind as {config}/agents/OLD.md
// once runGlobalAgents stops visiting it (it walks the CURRENT source
// roster, so a renamed-away identity is simply never revisited, never
// cleaned up on its own). frr -> rr (3976b53, "/ptm→/pfm, frr→rr, /rr→/deep-rr")
// is the only rename `git log -- templates/global/agents` holds; a future
// rename adds its OLD name here rather than inventing a second mechanism.
var retiredGlobalAgents = []string{"frr", "rr-super"}

// retireRenamedGlobalAgents deletes a stale pre-rename identity from the
// global claude agent registry — but only when the file is unambiguously
// the installer's own leftover, never a user's own agent that happens to
// reuse the retired filename:
//   - a symlink retires only when it targets a known Professor source;
//     unrelated personal links are preserved, including dangling ones;
//   - a regular file retires only when its YAML frontmatter `name:` field
//     still reads the retired name — the exact field RunGlobalAgents itself
//     keys the compiled TOML and the registry identity on, so a user who
//     repurposed the filename for their own agent (and so changed its
//     frontmatter name) is left alone, untouched and unreported.
func (installer *engine) retireRenamedGlobalAgents() error {
	repos, err := installer.recordedProfessorSourceRepos()
	if err != nil {
		installer.skip("renamed global agents skipped: " + err.Error() +
			" — rerun pfm install --yes from inside your Professor clone")
		return nil
	}
	repos = append(repos, filepath.Join(installer.options.Home, ".professor"))
	config := installer.options.ConfigDir
	for _, retired := range retiredGlobalAgents {
		path := filepath.Join(config, "agents", retired+".md")
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect retired global agent %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			owned := false
			for _, repo := range repos {
				if filepath.Clean(target) == filepath.Join(repo, "templates", "global", "agents", retired+".md") {
					owned = true
				}
			}
			if !owned {
				installer.skip(path + " is an unrelated personal agent link — left alone")
				continue
			}
		} else {
			frontmatterName, readErr := agentFrontmatterName(path)
			if readErr != nil {
				return fmt.Errorf("read retired global agent %s: %w", path, readErr)
			}
			if frontmatterName != retired {
				installer.skip(
					path + " is not the retired " + retired + " agent (frontmatter name=" + frontmatterName + ") — left alone",
				)
				continue
			}
		}
		if err := installer.retire(path, "renamed global agent ("+retired+" -> current roster)"); err != nil {
			return err
		}
	}

	return installer.retireRenamedCodexAgents()
}

// agentFrontmatterName reads the `name:` field out of an agent Markdown
// file's leading YAML frontmatter block, empty when the file has none or
// the field is absent — the same shape codexgen compiles agent identity
// from.
func agentFrontmatterName(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", nil
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if name, found := strings.CutPrefix(trimmed, "name:"); found {
			return strings.TrimSpace(name), nil
		}
	}
	return "", nil
}
