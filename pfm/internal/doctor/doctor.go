package doctor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"sort"
	"strings"
	"syscall"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/ask"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/harvest"
	"hostops/pfm/internal/harvestpy"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/professor"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/stats"
	"hostops/pfm/internal/store"
)

const emptySummary = "none"

const (
	doctorCommand   = "doctor"
	indexCommand    = "index"
	headlessCommand = "headless"
	askAction       = "ask"
	versionCommand  = "version"
)

// StateUnavailable is the stable diagnostic state for a probe that could not run.
const StateUnavailable = "unavailable"

// Dependencies are composition-root policies used by the doctor without
// importing the command package that owns them.
type Dependencies struct {
	ExpectedEngineCapabilities func(pfmengine.ID, []string) map[string]bool
	Clock                      clock.Clock
	Env                        paths.Env
	Runner                     deps.Runner
	Listen                     func(string, string) (net.Listener, error)
}

func normalizeDependencies(dependencies Dependencies) Dependencies {
	if dependencies.Clock == nil {
		dependencies.Clock = clock.Real
	}
	if dependencies.Env == nil {
		dependencies.Env = paths.OSEnv{}
	}
	if dependencies.Runner == nil {
		dependencies.Runner = obs.Runner(deps.RealRunner{})
	}
	if dependencies.Listen == nil {
		dependencies.Listen = net.Listen
	}
	return dependencies
}

// doctorTally is the two-tier count `runDoctor` threads through every row: warnings are advisory;
// failures mean `pfm install --yes` missed required state or a required dependency is unavailable.
// Only failures gate `pfm update` (update_command.go);
// warnings are reported as a delta against the pre-update baseline.
type doctorTally struct {
	warnings int
	failures int
}

func (t *doctorTally) warn() { t.warnings++ }
func (t *doctorTally) fail() { t.failures++ }

type harvestDoctor interface {
	Inspect(string, harvestpy.Platform) (harvestpy.EnvironmentDigest, error)
	Check(context.Context, string, harvestpy.Platform) (harvestpy.CheckReport, error)
}

// harvestDoctorOverride is nil in production. The command-package TestMain
// supplies a complete no-network fixture so existing doctor tests exercise
// fleet health without requiring a user-managed Python environment.
var HarvestOverride harvestDoctor

var DependencyProbeOverride func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result

// Run performs one health pass and returns 0 clean, 1 warnings, 2 usage, or 3 failures.
func Run(
	args []string,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	dependencies Dependencies,
) (exitCode int) {
	dependencies = normalizeDependencies(dependencies)
	flags := cli.NewFlagSet(
		doctorCommand,
		"usage: pfm doctor [--verbose] [--skip-harvest]   exit 0 clean, 1 warnings, 3 failures",
		stderr,
	)
	verbose := flags.Bool("verbose", false, "write raw dependency probe output under tmp/")
	skipHarvest := flags.Bool("skip-harvest", false, "exclude the optional harvestpy runtime from health")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	resolved := runtime.Paths
	tally := &doctorTally{}
	if runtime.ConfigError != nil {
		fmt.Fprintf(stdout, "doctor: config error=%v\n", runtime.ConfigError)
		tally.fail()
	}
	PrintConfig(stdout, runtime)
	tally.warnings += printHarvesterConfigDoctorWithEnv(stdout, runtime, dependencies.Env)
	tally.warnings += printDuplicateSeatLogins(stdout, runtime, dependencies.Env)
	tally.warnings += printEngineDoctor(stdout, runtime.Config)
	tally.warnings += printOpenCodeStoreDoctor(context.Background(), stdout, runtime.Config)
	tally.warnings += PrintEngineCapabilities(stdout, dependencies)
	tally.warnings += PrintMCPClientCutover(stdout, runtime)
	if mcpConfigured(runtime) {
		tally.warnings += printMCPDaemonDoctor(stdout, runtime)
	}
	database, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		fmt.Fprintf(stdout, "doctor: unhealthy database: %v\n", err)
		return 3
	}
	defer func() { cli.CloseResource(database, "doctor: close database", stderr, &exitCode) }()
	ctx := context.Background()
	pathWarnings := pfmPathWarningsWithEnv(resolved.Home, dependencies.Env.Get("PATH"), dependencies.Env)
	for _, warning := range pathWarnings {
		fmt.Fprintf(stdout, "doctor: warning %s\n", warning)
		if strings.HasPrefix(warning, "pfm_path_resolves=") || strings.HasPrefix(warning, "pfm_hash_mismatch=") {
			fmt.Fprintf(
				stdout,
				"doctor: remediation: put %s first on PATH and remove or rebuild stale pfm copies\n",
				filepath.Join(resolved.Home, ".local", "bin"),
			)
		}
	}
	tally.warnings += len(pathWarnings)
	if len(pathWarnings) == 0 {
		fmt.Fprintln(stdout, "doctor: path canonical")
	}
	printActivityLogDoctor(stdout, runtime)
	tally.warnings += printServiceManagerDoctor(ctx, stdout, dependencies.Runner)
	tally.warnings += printPrePushDoctorWithRunner(context.Background(), stdout, dependencies.Runner)
	verboseDir := ""
	if *verbose {
		verboseDir = filepath.Join("tmp", "pfm-doctor")
	}
	tally.warnings += printHarnessPromptDoctorWithDeps(
		context.Background(),
		stdout,
		resolved.Home,
		runtime.Config,
		verboseDir,
		dependencies,
	)
	primaryAccount, primaryErr := fleet.PrimaryAccount(resolved, runtime.Config)
	if primaryErr != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: read primary account: %v\n", primaryErr)
	}
	tally.warnings += printSpawnAuditDoctorWithClock(
		context.Background(),
		stdout,
		resolved,
		runtime.Config,
		primaryAccount,
		dependencies.Clock,
	)
	// INFO only, and it adds no warnings: both title owners are legitimate.
	printTmuxTitlesDoctorWithClock(context.Background(), stdout, resolved, runtime.Config, dependencies.Clock)
	launcher, launcherErr := installer.InspectClaudeLauncher(resolved.Home)
	if launcherErr != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: launcher: unreadable error=%v — run pfm install\n", launcherErr)
	} else {
		switch launcher.State {
		case installer.LauncherOK:
			fmt.Fprintln(stdout, "doctor: launcher: ok (pfm owns Claude Code version retention — see claude-versions)")
		case installer.LauncherMissing:
			tally.fail()
			fmt.Fprintln(stdout, "doctor: launcher: missing — run pfm install")
		case installer.LauncherDisplaced:
			tally.fail()
			fmt.Fprintf(stdout, "doctor: launcher: DISPLACED by %s — run pfm install\n", launcher.Target)
		default:
			tally.fail()
			fmt.Fprintf(stdout, "doctor: launcher: unknown state=%s — run pfm install\n", launcher.State)
		}
	}
	tally.warnings += printVSCodeDoctor(stdout, resolved.Home, runtime.Config)
	claudeVersionsWarnings, claudeVersionsFailures := printClaudeVersionsDoctor(
		stdout,
		resolved.Home,
		runtime.Config.Claude.Binary,
		gather.NewProcFS(resolved.ProcRoot),
	)
	tally.warnings += claudeVersionsWarnings
	tally.failures += claudeVersionsFailures
	depWarnings, depFailures, claudeAbsent := PrintDependencies(
		ctx,
		stdout,
		resolved.Home,
		deps.Registry(deps.Options{
			Home: resolved.Home, ClaudeBinary: runtime.Config.Claude.Binary, CodexBinary: runtime.Config.Codex.Binary,
		}),
		deps.ProbeOptions{VerboseDir: verboseDir, SkipHarvest: *skipHarvest, Runner: obs.Runner(deps.RealRunner{})},
	)
	tally.warnings += depWarnings
	tally.failures += depFailures
	overlayWarnings, overlayFailures := printHostOverlayDoctor(stdout, resolved.Home, runtime.Config)
	tally.warnings += overlayWarnings
	tally.failures += overlayFailures
	globalAgentsWarnings, globalAgentsFailures := installer.ReportGlobalAgents(
		stdout,
		resolved.Home,
		runtime.Config.Accounts,
		claudeAbsent,
	)
	tally.warnings += globalAgentsWarnings
	tally.failures += globalAgentsFailures
	hookWarnings, hookFailures := installer.ReportHooks(stdout, resolved.Home, runtime.Config, claudeAbsent)
	tally.warnings += hookWarnings
	tally.failures += hookFailures

	version, err := database.UserVersion(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: unhealthy user_version: %v\n", err)
		return 3
	}
	check, err := database.QuickCheck(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: unhealthy integrity: %v\n", err)
		return 3
	}
	if version != store.SchemaVersion || check != "ok" {
		tally.warn()
	}
	fmt.Fprintf(
		stdout,
		"doctor: database user_version=%d expected=%d quick_check=%s\n",
		version,
		store.SchemaVersion,
		check,
	)

	// Kills live in the fleet's shared database, not this binary's cache.
	sharedState := "ok"
	if degraded := database.SharedDegraded(); degraded != nil {
		tally.warn()
		sharedState = degraded.Error()
	}
	fmt.Fprintf(
		stdout,
		"doctor: shared store=%s state=%s\n",
		database.SharedPath(),
		sharedState,
	)

	counts, err := database.Counts(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: unhealthy row counts: %v\n", err)
		return 3
	}
	if counts.OrphanedKills != 0 {
		tally.warn()
	}
	fmt.Fprintf(
		stdout,
		"doctor: rows transcripts=%d rollouts=%d cx_names=%d killed=%d orphaned_killed=%d\n",
		counts.Transcripts,
		counts.Rollouts,
		counts.CxNames,
		counts.Killed,
		counts.OrphanedKills,
	)

	walBytes := int64(0)
	if info, err := os.Stat(database.Path() + "-wal"); err == nil {
		walBytes = info.Size()
	} else if !os.IsNotExist(err) {
		tally.warn()
		fmt.Fprintf(stdout, "doctor: warning WAL stat: %v\n", err)
	}
	fmt.Fprintf(stdout, "doctor: wal_bytes=%d\n", walBytes)

	killWarnings, err := metaCounter(ctx, database, "busy_kill_warnings")
	if err != nil {
		fmt.Fprintf(stdout, "doctor: unhealthy busy counter: %v\n", err)
		return 3
	}
	unkillWarnings, err := metaCounter(ctx, database, "busy_unkill_warnings")
	if err != nil {
		fmt.Fprintf(stdout, "doctor: unhealthy busy counter: %v\n", err)
		return 3
	}
	if killWarnings != 0 || unkillWarnings != 0 {
		tally.warn()
	}
	fmt.Fprintf(
		stdout,
		"doctor: busy_warnings kill=%d unkill=%d\n",
		killWarnings,
		unkillWarnings,
	)

	// The process table is probed by READING it, not by stat'ing /proc. macOS has
	// no /proc and never will — pfm reads its process table through sysctl there
	// — so a directory check reports a healthy machine as broken, and on Linux it
	// proves less than the read does: a /proc that exists but denies the read is
	// the failure that actually matters.
	if pids, procErr := gather.NewProcFS(resolved.ProcRoot).PIDs(); procErr != nil {
		tally.warn()
		fmt.Fprintf(stdout, "doctor: warning process_table unreadable: %v\n", procErr)
	} else {
		fmt.Fprintf(stdout, "doctor: process_table readable pids=%d\n", len(pids))
	}

	tally.warnings += config.ReportRoots(stdout, runtime.Config.Accounts, runtime.Config.CodexAccounts, claudeAbsent)
	tally.warnings += professor.PrintDoctor(stdout, ".", resolved.Home)

	tally.warnings += PrintCodexPaneBinding(ctx, stdout, database, runtime)

	crumbEntries, crumbInvalid, crumbErr := crumbHealth(resolved.SIDDir)
	if crumbErr != nil {
		tally.warn()
		fmt.Fprintf(stdout, "doctor: warning crumb_dir=%v\n", crumbErr)
	} else {
		if crumbInvalid != 0 {
			tally.warn()
		}
		fmt.Fprintf(
			stdout,
			"doctor: crumbs entries=%d invalid=%d\n",
			crumbEntries,
			crumbInvalid,
		)
	}
	if *skipHarvest {
		fmt.Fprintln(stdout, "doctor: harvestpy skipped (--skip-harvest)")
	} else {
		tally.warnings += printHarvestPythonDoctorWithRunner(
			ctx,
			stdout,
			resolved.Home,
			harvestpy.Platform{},
			configuredHarvestDoctor(),
			runtime.Config.Harvester.Fetch.Browser,
			dependencies.Runner,
		)
	}
	tally.warnings += printHarvestCacheDoctor(stdout, runtime.Config.Harvester)
	tally.warnings += printHarvestSearchDoctor(ctx, stdout, runtime.Config.Harvester)
	if tally.failures > 0 {
		fmt.Fprintf(stdout, "doctor: failures=%d\n", tally.failures)
	}
	if tally.warnings > 0 {
		fmt.Fprintf(stdout, "doctor: warnings=%d\n", tally.warnings)
	}
	switch {
	case tally.failures > 0:
		return 3
	case tally.warnings > 0:
		return 1
	}
	fmt.Fprintln(stdout, "doctor: clean")
	return 0
}

// printCodexPaneBindingDoctor audits the Codex /clear bindings against each
// other. It is the answer to "what does this instrument report when it is
// itself broken": a single binding read alone always looks fine, so the two
// states that actually break /clear are only visible in the relations —
//
//   - contested: two live panes bound to ONE thread, which means at least one
//     pane is being followed into a chat it is not running;
//   - retired: a pane bound to a thread a clear already killed, which is where
//     `pfm chat resolve` starts answering with a corpse and every inject lands
//     in a thread nobody is in.
//
// Both were found on a real host by reading the meta table by hand. Neither
// produced a single line of output anywhere in pfm. They do now.
func PrintCodexPaneBinding(
	ctx context.Context,
	stdout io.Writer,
	database *store.Store,
	runtime config.Runtime,
) int {
	manager, err := kill.New(database, fleet.KillDependencies(runtime))
	if err != nil {
		fmt.Fprintf(stdout, "doctor: warning codex_pane_bindings=unreadable error=%v\n", err)
		return 1
	}
	bindings, err := manager.CodexPaneBindings(ctx)
	if err != nil {
		// An unreadable table is not an empty one. Reporting "0 bindings"
		// here would be the same word a genuinely clean fleet prints.
		fmt.Fprintf(stdout, "doctor: warning codex_pane_bindings=unreadable error=%v\n", err)
		return 1
	}

	// The binding table outlives the panes: a socket that is gone leaves its
	// row behind, and this host held 78 bindings for 19 live panes. Counting
	// those as "contested" would report a fleet-wide emergency made almost
	// entirely of litter — a check that cries wolf is the same failure as one
	// that stays silent, just louder.
	live := make(map[string]bool)
	snapshot, paneErr := liveCodexSnapshot(ctx, runtime, manager)
	panes := snapshot.Panes
	if paneErr != nil {
		// A pane list that could not be read is not an empty one. Without it,
		// nothing can be called stale, so the contested count is left over the
		// whole table and the uncertainty is stated rather than hidden.
		fmt.Fprintf(stdout, "doctor: warning codex_panes=unreadable error=%v\n", paneErr)
	}
	for paneIndex := range panes {
		pane := &panes[paneIndex]
		live[pane.Socket+" "+pane.PaneID] = true
	}
	knowLive := paneErr == nil

	panesByThread := make(map[string][]string, len(bindings))
	undecodable := 0
	stale := 0
	for _, binding := range bindings {
		if binding.Socket == "" {
			undecodable++
			continue
		}
		address := binding.Socket + " " + binding.PaneID
		if knowLive && !live[address] {
			stale++
			continue
		}
		panesByThread[binding.ThreadID] = append(panesByThread[binding.ThreadID], address)
	}

	warnings := 0
	contested := 0
	threads := make([]string, 0, len(panesByThread))
	for thread := range panesByThread {
		threads = append(threads, thread)
	}
	sort.Strings(threads)
	for _, thread := range threads {
		if len(panesByThread[thread]) < 2 {
			continue
		}
		contested++
		warnings++
		fmt.Fprintf(
			stdout,
			"doctor: warning codex_pane_binding=contested thread=%s panes=%s\n",
			thread, strings.Join(panesByThread[thread], ","),
		)
	}

	retired := 0
	for _, thread := range threads {
		record, killed, err := database.Killed(ctx, thread)
		if err != nil {
			warnings++
			fmt.Fprintf(
				stdout,
				"doctor: warning codex_pane_binding=kill-state-unreadable thread=%s error=%v\n",
				thread, err,
			)
			continue
		}
		if !killed || record.BaselinePrompts == nil {
			continue
		}
		retired++
		warnings++
		fmt.Fprintf(
			stdout,
			"doctor: warning codex_pane_binding=retired-thread thread=%s panes=%s\n",
			thread, strings.Join(panesByThread[thread], ","),
		)
	}
	if undecodable != 0 {
		warnings++
		fmt.Fprintf(stdout, "doctor: warning codex_pane_binding=undecodable count=%d\n", undecodable)
	}
	// stale is REPORTED, never warned about: a binding whose pane is gone is
	// ordinary history, and the next pass on that socket overwrites it. It is
	// counted so the totals reconcile — a reader who sees total=78 and
	// contested=2 must be able to see where the other 76 went.
	fmt.Fprintf(
		stdout,
		"doctor: codex_pane_bindings total=%d live=%d stale=%d contested=%d retired=%d undecodable=%d\n",
		len(bindings), len(bindings)-stale-undecodable, stale, contested, retired, undecodable,
	)
	warnings += PrintCodexPaneFollow(ctx, stdout, database, manager, runtime, snapshot, paneErr)
	if warnings != 0 {
		fmt.Fprintf(
			stdout,
			"doctor: remediation: a RETIRED binding is dropped automatically on the next gather "+
				"pass (`pfm ls`), which returns the pane to unbound so its own status line can "+
				"re-seat it — unless that status line already shows a NAME resolving only to "+
				"retired threads (%q); that pane cannot re-seat on its own, because a name never "+
				"moves a binding and every thread it names is dead. Its remedy is giving the pane "+
				"an identity pfm can observe again: rename it onto a live thread, or /clear it, "+
				"which is the one input Codex renders as a bare thread id; a CONTESTED binding "+
				"resolves as soon as either pane shows a bare thread id, which happens on its next "+
				"/clear\n",
			fleet.CodexPaneNameRetired,
		)
	}
	return warnings
}

// printCodexPaneFollowDoctor reports the panes pfm cannot currently follow
// through a /clear.
//
// The reconcile pass stays SILENT about these: it runs on every picker refresh,
// and an unindexed name resolves itself the moment Codex's index is re-read, so
// warning there trains the reader to ignore the channel. Silent on the hot path
// is only defensible if something else says it out loud on request — and this
// is that something. A pane listed here is a pane whose next /clear will go
// unnoticed.
//
// It runs the SAME decision the reconcile pass runs, over a fresh capture, so
// the report cannot drift from the behaviour it describes.
func PrintCodexPaneFollow(
	ctx context.Context,
	stdout io.Writer,
	database *store.Store,
	manager *kill.Manager,
	runtime config.Runtime,
	snapshot gather.Snapshot,
	paneErr error,
) int {
	if paneErr != nil {
		// "No live panes" and "we could not look for live panes" are different
		// answers and must not print the same word. The caller already named
		// the failure; this only refuses to report coverage it does not have.
		return 1
	}
	if len(snapshot.Panes) == 0 {
		fmt.Fprintln(stdout, "doctor: codex_panes live=0")
		return 0
	}
	cxNames, err := database.CxNames(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: warning codex_pane_names=unreadable error=%v\n", err)
		return 1
	}
	capturer := gather.TmuxProbe{TmuxTmpDir: filepath.Dir(runtime.Paths.TmuxDir)}
	silent := func(message string) { fmt.Fprintf(stdout, "doctor: warning %s\n", message) }
	_, actions := fleet.ObserveCodexPanes(
		ctx, database, manager, capturer, snapshot, runtime, cxNames, silent,
	)

	unfollowable := 0
	warnings := 0
	for actionIndex := range actions {
		observedAction := &actions[actionIndex]
		switch observedAction.Skip {
		case "", fleet.CodexPaneSameLineage:
			continue
		}
		unfollowable++
		warnings++
		fmt.Fprintf(
			stdout,
			"doctor: warning codex_pane=unfollowable socket=%s pane=%s reason=%q\n",
			observedAction.Socket, observedAction.PaneID, observedAction.Skip,
		)
	}
	fmt.Fprintf(
		stdout,
		"doctor: codex_panes live=%d unfollowable=%d\n", len(actions), unfollowable,
	)
	if unfollowable != 0 {
		fmt.Fprintln(
			stdout,
			"doctor: remediation: an unfollowable pane's next /clear goes unnoticed — "+
				"name the chat with `pfm chat name`, or wait one index refresh if the chat "+
				"was renamed moments ago",
		)
	}
	return warnings
}

// liveCodexPanes enumerates the Codex panes on the fleet's private tmux
// directory. It exists so `pfm doctor` can audit pane state without paying for
// a whole fleet gather (procfs walk, every engine, every transcript) that it
// would use one field of.
func liveCodexPanes(ctx context.Context, runtime config.Runtime) ([]gather.ProbePane, error) {
	entries, err := os.ReadDir(runtime.Paths.TmuxDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No tmux directory is a real, knowable answer: no fleet has run.
			return nil, nil
		}
		return nil, fmt.Errorf("read tmux directory %s: %w", runtime.Paths.TmuxDir, err)
	}
	tmux := gather.TmuxProbe{TmuxTmpDir: filepath.Dir(runtime.Paths.TmuxDir)}
	panes := make([]gather.ProbePane, 0, len(entries))
	for _, entry := range entries {
		socket := entry.Name()
		if id, ok := pfmengine.FromSocket(socket); !ok || id != pfmengine.Codex {
			continue
		}
		found, err := tmux.ListPanes(ctx, socket)
		if err != nil {
			// A dead socket file is ordinary litter, not a probe failure worth
			// failing the whole audit over.
			continue
		}
		panes = append(panes, found...)
	}
	return panes, nil
}

func printOpenCodeStoreDoctor(ctx context.Context, stdout io.Writer, machine config.Config) int {
	if len(machine.OpenCodeAccounts) == 0 {
		fmt.Fprintln(stdout, "doctor: opencode store=absent")
		return 0
	}
	warnings := 0
	for _, account := range machine.OpenCodeAccounts {
		if err := index.ProbeOpenCodeStore(ctx, account.Home); err != nil {
			warnings++
			fmt.Fprintf(stdout, "doctor: opencode store=unhealthy account=%d error=%v\n", account.ID, err)
			continue
		}
		fmt.Fprintf(stdout, "doctor: opencode store=healthy account=%d\n", account.ID)
	}
	return warnings
}

func printEngineDoctor(stdout io.Writer, machine config.Config) int {
	counts := machine.Engines()
	defaultEngine, err := machine.DefaultEngine()
	parts := make([]string, 0, len(pfmengine.All()))
	for _, id := range pfmengine.All() {
		parts = append(parts, fmt.Sprintf("%s=%d", id, counts[id]))
	}
	if err != nil {
		fmt.Fprintf(stdout, "doctor: roster %s default=none error=%v\n", strings.Join(parts, " "), err)
		return 1
	}
	fmt.Fprintf(stdout, "doctor: roster %s default=%s\n", strings.Join(parts, " "), defaultEngine)
	return 0
}

func PrintEngineCapabilities(stdout io.Writer, dependencies Dependencies) int {
	capabilities := []struct {
		name string
		ids  []pfmengine.ID
	}{
		{name: indexCommand, ids: index.RegisteredSources()},
		{name: "launcher", ids: spawn.RegisteredLaunchers()},
		{name: "matcher", ids: gather.RegisteredMatchers()},
		{name: "usage", ids: stats.RegisteredUsageSources()},
		{name: headlessCommand, ids: action.RegisteredPlanners()},
		{name: askAction, ids: ask.RegisteredRunners()},
	}
	parts := make([]string, 0, len(pfmengine.All()))
	warnings := 0
	allNames := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		allNames = append(allNames, capability.name)
	}
	for _, id := range pfmengine.All() {
		registered := make([]string, 0, len(capabilities))
		actual := make(map[string]bool, len(capabilities))
		for _, capability := range capabilities {
			if containsEngine(capability.ids, id) {
				registered = append(registered, capability.name)
				actual[capability.name] = true
			}
		}
		expected := map[string]bool{}
		if dependencies.ExpectedEngineCapabilities != nil {
			expected = dependencies.ExpectedEngineCapabilities(id, allNames)
		}
		missing := make([]string, 0)
		unexpected := make([]string, 0)
		for _, name := range allNames {
			if expected[name] && !actual[name] {
				missing = append(missing, name)
			}
			if !expected[name] && actual[name] {
				unexpected = append(unexpected, name)
			}
		}
		row := fmt.Sprintf("%s=%s", id, strings.Join(registered, ","))
		if len(registered) == 0 {
			row = fmt.Sprintf("%s=NONE", id)
		}
		if len(missing) != 0 || len(unexpected) != 0 {
			if len(missing) != 0 {
				row += " MISSING(" + strings.Join(missing, ",") + ")"
			}
			if len(unexpected) != 0 {
				row += " UNEXPECTED(" + strings.Join(unexpected, ",") + ")"
			}
			warnings++
		}
		parts = append(parts, row)
	}
	fmt.Fprintf(stdout, "doctor: engines %s\n", strings.Join(parts, " "))
	return warnings
}

func containsEngine(ids []pfmengine.ID, want pfmengine.ID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func configuredDependencyProbe(ctx context.Context, entries []deps.Entry, options deps.ProbeOptions) []deps.Result {
	if DependencyProbeOverride != nil {
		return DependencyProbeOverride(ctx, entries, options)
	}
	return deps.Probe(ctx, entries, options)
}

func PrintDependencies(
	ctx context.Context,
	stdout io.Writer,
	home string,
	entries []deps.Entry,
	options deps.ProbeOptions,
) (warnings, failures int, claudeAbsent bool) {
	results := configuredDependencyProbe(ctx, entries, options)
	for resultIndex := range results {
		result := &results[resultIndex]
		entry := result.Entry
		// A dependency the fleet engine itself cannot run without (Required,
		// and not one of the opt-in harvestpy sidecar's own entries) is a
		// FAILURE; the sidecar's own Required-within-itself deps (uv, the
		// provisioned interpreter) stay warnings — the fleet engine runs
		// fine without them, and `pfm install --skip-harvest` is pfm's own
		// decision not to provision them, not a state it failed to produce.
		gatesEngine := entry.Required && !entry.Harvest
		switch result.State {
		case deps.StateSkipped:
			platform := strings.Join(entry.Platforms, ",")
			if platform == "" {
				platform = "all"
			}
			fmt.Fprintf(stdout, "doctor: dep %s platform=%s skipped (%s)\n", entry.Name, platform, result.Error)
		case deps.StateMissing:
			requirement := "optional"
			if entry.Required {
				requirement = "required"
				if gatesEngine {
					failures++
				} else {
					warnings++
				}
			}
			fmt.Fprintf(
				stdout,
				"doctor: dep %s path=(none) MISSING %s — install: %s\n",
				entry.Name,
				requirement,
				entry.InstallHint,
			)
		case deps.StateBroken:
			if entry.Engine == pfmengine.Claude && installer.ClaudeAbsent(home, result.Path, result.ExitCode) {
				claudeAbsent = true
				fmt.Fprintf(
					stdout,
					"doctor: dep %s path=%s MISSING optional — install: install Claude Code (the pfm launcher has no real binary to run)\n",
					entry.Name,
					result.Path,
				)
				continue
			} else if entry.Required {
				if gatesEngine {
					failures++
				} else {
					warnings++
				}
			}
			raw := deps.FirstLine(result.Raw)
			if raw != "" && !strings.Contains(result.Error, "raw=") {
				fmt.Fprintf(
					stdout,
					"doctor: dep %s path=%s broken error=%s raw=%q\n",
					entry.Name,
					result.Path,
					result.Error,
					raw,
				)
			} else {
				fmt.Fprintf(stdout, "doctor: dep %s path=%s broken error=%s\n", entry.Name, result.Path, result.Error)
			}
		case deps.StateTimeout:
			// A probe that outran its bound answered nothing; it did not answer
			// broken. The requirement is still unverified, so a required dep
			// keeps its failure and preflight still refuses — the arithmetic is
			// unchanged — but the line must never send the reader to reinstall a
			// tool whose only symptom was being slow. It must also not carry the
			// word this state is not: doctor output gets grepped, and a line
			// reading "not broken" is counted by `grep -c broken` as a break.
			if entry.Required {
				if gatesEngine {
					failures++
				} else {
					warnings++
				}
			}
			fmt.Fprintf(
				stdout,
				"doctor: dep %s path=%s timeout error=%s — the binary resolved and was executed but answered nothing within the bound; unverified, no fault established\n",
				entry.Name,
				result.Path,
				result.Error,
			)
		case deps.StateCancelled:
			// The caller stopped the probe before it answered. Keep the required
			// dependency failure arithmetic unchanged, but name the parent context
			// as the cause rather than diagnosing the resolved binary.
			if entry.Required {
				if gatesEngine {
					failures++
				} else {
					warnings++
				}
			}
			fmt.Fprintf(
				stdout,
				"doctor: dep %s path=%s cancelled error=%s — probe stopped by its caller; unverified, no fault established\n",
				entry.Name,
				result.Path,
				result.Error,
			)
		case deps.StateOK:
			fmt.Fprintf(stdout, "doctor: dep %s path=%s", entry.Name, result.Path)
			if result.Version != "" {
				fmt.Fprintf(stdout, " version=%s", result.Version)
			}
			if entry.MinVersion != "" {
				fmt.Fprintf(stdout, " min=%s", entry.MinVersion)
			}
			if result.SelfDoctor != "" {
				fmt.Fprintf(stdout, " self_doctor=%s", result.SelfDoctor)
			}
			fmt.Fprintln(stdout, " ok")
		default:
			warnings++
			fmt.Fprintf(stdout, "doctor: dep %s broken error=unknown probe state %q\n", entry.Name, result.State)
		}
		if result.VerboseErr != "" {
			warnings++
			fmt.Fprintf(stdout, "doctor: dep %s verbose broken error=%s\n", entry.Name, result.VerboseErr)
		}
	}
	return warnings, failures, claudeAbsent
}

// printHostOverlayDoctor checks the two contracted ~/.local/bin overlay
// scripts pfm install owns (installer.InspectHostOverlays), and — for every
// configured Claude account's settings.json — that statusLine.command names
// the pfm-statusline overlay rather than the raw `pfm statusline` an
// unwired or pre-overlay install leaves behind. Every non-clean state here
// is a FAILURE (warnings++), never a soft note: an absent or misdirected
// overlay renders identically to a healthy plain statusline (issue #14 F1)
// — the failure is invisible from the prompt itself, so doctor has to be
// the thing that notices it.
// printHostOverlayDoctor treats every non-clean row — a missing/displaced/
// unknown overlay symlink and a settings.json statusLine.command still
// naming the raw `pfm statusline` — as a FAILURE, never a soft warning: a
// misdirected or absent overlay is invisible from the prompt itself (issue
// #14 F1), and only a state `pfm install --yes` is responsible for producing
// is reported here at all.
func printHostOverlayDoctor(stdout io.Writer, home string, machine config.Config) (warnings, failures int) {
	for _, overlay := range installer.InspectHostOverlays(home) {
		switch overlay.State {
		case installer.HostOverlayOK:
			fmt.Fprintf(stdout, "doctor: host_overlay %s ok\n", overlay.Name)
		case installer.HostOverlayMissing:
			failures++
			fmt.Fprintf(stdout, "doctor: host_overlay %s missing — run pfm install --yes\n", overlay.Name)
		case installer.HostOverlayDisplaced:
			failures++
			fmt.Fprintf(
				stdout,
				"doctor: host_overlay %s DISPLACED by %s — run pfm install --yes\n",
				overlay.Name,
				overlay.Target,
			)
		default:
			failures++
			fmt.Fprintf(
				stdout,
				"doctor: host_overlay %s unknown state=%s — run pfm install --yes\n",
				overlay.Name,
				overlay.State,
			)
		}
	}
	failures += printStatusLineOverlayDoctor(stdout, home, machine)
	return warnings, failures
}

// printClaudeVersionsDoctor reports the growth pfm's launcher causes by
// disabling Claude Code's own version cleanup (see internal/installer's
// claude_versions.go): count, total bytes, the newest build, any build a
// live process is executing, and how much a prune would free. Absence — no
// native Claude installer ever ran here — is never a warning; a probe
// failure is, because retention then reads as unknown rather than clean.
func printClaudeVersionsDoctor(
	stdout io.Writer,
	home, configuredBinary string,
	procs gather.ProcFS,
) (warnings, failures int) {
	report, err := installer.InspectClaudeVersions(home, configuredBinary)
	if err != nil {
		failures++
		fmt.Fprintf(stdout, "doctor: claude-versions unreadable error=%v — run pfm install\n", err)
		return warnings, failures
	}
	if len(report.Versions) == 0 {
		fmt.Fprintf(stdout, "doctor: claude-versions dir=%s absent (native installer never ran)\n", report.Dir)
		return warnings, failures
	}
	report = installer.ProbeLiveClaudeVersions(report, procs, syscall.Kill)
	if report.LiveProbeErr != nil {
		warnings++
		fmt.Fprintf(stdout, "doctor: claude-versions PROBE FAILED error=%v — retention unknown\n", report.LiveProbeErr)
		return warnings, failures
	}
	var totalBytes int64
	for _, version := range report.Versions {
		totalBytes += version.Bytes
	}
	remove, _ := installer.PlanClaudeVersionPrune(report, installer.ClaudeVersionKeepCount)
	var prunableBytes int64
	for _, version := range remove {
		prunableBytes += version.Bytes
	}
	newest := emptySummary
	if report.Newest != nil {
		newest = filepath.Base(report.Newest.Path)
	}
	liveText := emptySummary
	if len(report.Live) > 0 {
		parts := make([]string, 0, len(report.Live))
		for path, pids := range report.Live {
			parts = append(parts, fmt.Sprintf("%s(%d pids)", filepath.Base(path), len(pids)))
		}
		sort.Strings(parts)
		liveText = strings.Join(parts, ",")
	}
	line := fmt.Sprintf(
		"doctor: claude-versions dir=%s count=%d bytes=%s newest=%s live=%s prunable=%d (%s)",
		report.Dir, len(report.Versions), installer.FormatClaudeVersionBytes(totalBytes), newest, liveText,
		len(remove), installer.FormatClaudeVersionBytes(prunableBytes),
	)
	if len(remove) > 0 {
		warnings++
		line += " — run pfm install --yes to prune"
	}
	fmt.Fprintln(stdout, line)
	return warnings, failures
}

func printHarvestCacheDoctor(stdout io.Writer, harvester config.HarvesterConfig) int {
	root, rootErr := harvest.CacheRoot(harvester.Cache.Dir)
	if rootErr != nil {
		// An unresolvable root is "we failed to look", never "0 entries".
		fmt.Fprintf(stdout, "doctor: harvester_cache dir=? error=%v\n", rootErr)
		return 1
	}
	entries := 0
	walkErr := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			entries++
		}
		return nil
	})
	if os.IsNotExist(walkErr) {
		walkErr = nil
	}
	ttlText := harvester.Cache.TTL.String()
	if harvester.Cache.TTL == 0 {
		ttlText = "never"
	}
	if walkErr != nil {
		fmt.Fprintf(
			stdout,
			"doctor: harvester_cache dir=%s entries=%d ttl=%s error=%v\n",
			root,
			entries,
			ttlText,
			walkErr,
		)
		return 1
	}
	fmt.Fprintf(stdout, "doctor: harvester_cache dir=%s entries=%d ttl=%s\n", root, entries, ttlText)
	return 0
}

// printHarvestSearchDoctor prints the search tool's one health line; the
// probe itself (OFF/reachable/UNREACHABLE/configured classification) lives in
// harvest.ProbeSearch, never here — cmd/pfm is dispatch, not policy (C3).
func printHarvestSearchDoctor(ctx context.Context, stdout io.Writer, harvester config.HarvesterConfig) int {
	probe := harvest.ProbeSearch(ctx, harvest.SearchOptions{
		SearXNGURL:    harvester.Search.SearXNGURL,
		BraveAPIKey:   harvester.Search.BraveAPIKey,
		DisableSearch: !harvester.Search.Enabled,
	}, nil)
	fmt.Fprintf(
		stdout,
		"doctor: harvester search state=%s backend=%s detail=%s\n",
		probe.State,
		probe.Backend,
		probe.Detail,
	)
	if probe.Warning {
		return 1
	}
	return 0
}

func mcpConfigured(runtime config.Runtime) bool {
	for _, server := range runtime.Config.MCPServers {
		if server.Enabled {
			return true
		}
	}
	return false
}

func configuredHarvestDoctor() harvestDoctor {
	if HarvestOverride != nil {
		return HarvestOverride
	}
	return pinnedHarvestDoctor{}
}

func printHarvestPythonDoctor(
	ctx context.Context,
	stdout io.Writer,
	home string,
	platform harvestpy.Platform,
	doctor harvestDoctor,
	browserGate bool,
) int {
	return printHarvestPythonDoctorWithRunner(
		ctx, stdout, home, platform, doctor, browserGate, obs.Runner(deps.RealRunner{}),
	)
}

func printHarvestPythonDoctorWithRunner(
	ctx context.Context,
	stdout io.Writer,
	home string,
	platform harvestpy.Platform,
	doctor harvestDoctor,
	browserGate bool,
	runner deps.Runner,
) int {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	if pinned, ok := doctor.(pinnedHarvestDoctor); ok {
		pinned.runner = runner
		doctor = pinned
	}
	if platform.GOOS == "" {
		platform.GOOS, platform.GOARCH = goRuntime.GOOS, goRuntime.GOARCH
	}
	root := filepath.Join(home, ".local", "state", "pfm", "harvest-python")
	current := harvestpy.RuntimeRoot(root, platform)
	interpreter := filepath.Join(current, "project", ".venv", "bin", "python")
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stdout, "doctor: harvestpy skipped")
		// The conversion env is absent (honest absence), but the opt-in
		// browser row must still report: with fetch.browser on a missing
		// environment is the NOT_PROVISIONED state, never silence.
		return appendHarvestBrowserDoctorRowWithRunner(
			ctx,
			stdout,
			root,
			platform,
			0,
			browserGate,
			runner,
			func() string {
				return resolveChromeForDoctorWithRunner(runner)
			},
		)
	}
	warnings := 0

	plan, planErr := harvestpy.PlanConversionEnvironment(platform)
	if planErr != nil {
		fmt.Fprintf(stdout, "doctor: harvestpy pinned_version=(default) unavailable error=%v\n", planErr)
		warnings++
	} else {
		fmt.Fprintf(stdout, "doctor: harvestpy pinned_version=(default) %s\n", plan.PythonVersion)
		if len(plan.PackageBlockers) != 0 || plan.PackageDownloadStatus == "blocked-exact-lock" {
			warnings++
			fmt.Fprintf(stdout, "doctor: harvestpy package_plan=(default) blocked-exact-lock\n")
			for _, blocker := range plan.PackageBlockers {
				fmt.Fprintf(stdout, "doctor: harvestpy package_blocker=%s\n", blocker)
			}
		}
	}

	digest, inspectErr := doctor.Inspect(root, platform)
	report, checkErr := doctor.Check(ctx, root, platform)
	if report.Digest.Python != "" {
		digest = report.Digest
	}

	interpreterStatus, interpreterErr := harvestDoctorCheck(report, "interpreter", checkErr)
	if interpreterStatus {
		fmt.Fprintf(stdout, "doctor: harvestpy interpreter=(file) %s version=%s\n", interpreter, digest.Python)
	} else {
		warnings++
		fmt.Fprintf(stdout, "doctor: harvestpy interpreter=(file) %s broken error=%s\n", interpreter, interpreterErr)
	}
	if inspectErr != nil && digest.LockSHA256 == "" {
		warnings++
		fmt.Fprintf(stdout, "doctor: harvestpy marker=(file) broken error=%v\n", inspectErr)
	}

	// "incomplete" is the word reserved for a marker left by an interrupted
	// provision (digest.State == "incomplete"); any other lock/inventory
	// failure is a live check failure against a provision that DID finish —
	// a decode error, a stale lock, a dependency drift — and that is
	// "broken", never "incomplete".
	harvestFailureWord := brokenState
	if digest.State == "incomplete" {
		harvestFailureWord = "incomplete"
	}

	lockOK, lockErr := harvestDoctorCheck(report, "lock_hash", checkErr)
	if lockOK && digest.LockSHA256 != "" {
		fmt.Fprintf(stdout, "doctor: harvestpy lock=(file) complete digest=%s\n", digest.LockSHA256)
	} else {
		warnings++
		if lockErr == "" {
			lockErr = "lock digest or completeness check is missing"
		}
		fmt.Fprintf(stdout, "doctor: harvestpy lock=(file) %s error=%s\n", harvestFailureWord, lockErr)
	}

	inventoryOK, inventoryErr := harvestDoctorCheck(report, "lock_completeness", checkErr)
	if inventoryOK && digest.InventorySHA256 != "" && digest.InventoryCount > 0 {
		fmt.Fprintf(
			stdout,
			"doctor: harvestpy inventory=(file) complete count=%d digest=%s\n",
			digest.InventoryCount,
			digest.InventorySHA256,
		)
	} else {
		warnings++
		if inventoryErr == "" {
			inventoryErr = "installed inventory digest/count is missing"
		}
		fmt.Fprintf(stdout, "doctor: harvestpy inventory=(file) %s error=%s\n", harvestFailureWord, inventoryErr)
	}

	smokeOK, smokeErr := harvestDoctorCheck(report, "live_smoke", checkErr)
	conversionOK, conversionErr := harvestDoctorCheck(report, "live_smoke_conversion", checkErr)
	if smokeOK && conversionOK {
		fmt.Fprintln(stdout, "doctor: harvestpy live_smoke=(file) healthy")
	} else {
		warnings++
		if smokeErr == "" {
			smokeErr = conversionErr
		}
		if smokeErr == "" {
			smokeErr = "live smoke check did not pass"
		}
		fmt.Fprintf(stdout, "doctor: harvestpy live_smoke=(file) broken error=%s\n", smokeErr)
	}
	warnings += printUnnamedHarvestChecks(stdout, report, harvestNamedDoctorChecks)
	return appendHarvestBrowserDoctorRowWithRunner(
		ctx,
		stdout,
		root,
		platform,
		warnings,
		browserGate,
		runner,
		func() string {
			return resolveChromeForDoctorWithRunner(runner)
		},
	)
}

// resolveChromeForDoctor re-checks the Google Chrome locations the browser
// worker itself resolves (browser.py CHROME_CANDIDATES), without launching
// anything. Chromium variants are deliberately absent: channel="chrome"
// launches only GOOGLE Chrome, so reporting a chromium-only host as healthy
// would pass smoke and fail every launch.
func resolveChromeForDoctor() string {
	return resolveChromeForDoctorWithRunner(obs.Runner(deps.RealRunner{}))
}

func resolveChromeForDoctorWithRunner(runner deps.Runner) string {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	for _, candidate := range []string{
		"google-chrome", "google-chrome-stable",
		"/usr/bin/google-chrome", "/usr/bin/google-chrome-stable",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	} {
		if strings.ContainsRune(candidate, filepath.Separator) {
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate
			}
			continue
		}
		if path, err := runner.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

// appendHarvestBrowserDoctorRow reports the opt-in real-browser rung
// (Patchright + system Chrome). Its broken states are deliberately distinct:
// NOT provisioned ≠ provisioned-but-Chrome-missing ≠ probe failed. With the
// fetch.browser gate off the row is informational only — absence of an
// opt-in environment is not a defect.
// doctorChromeResolver is injectable so tests can simulate a Chrome-less
// host without depending on the machine they run on.
var doctorChromeResolver = resolveChromeForDoctor

func browserEnvFingerprint(digest harvestpy.EnvironmentDigest) string {
	if len(digest.Digest) >= 8 {
		return digest.Digest[:8]
	}
	return StateUnknown
}

// gateOn is harvester.config.json fetch.browser — the same value the core's
// ladder receives, so doctor and the harvester can never disagree.
func appendHarvestBrowserDoctorRow(
	ctx context.Context,
	stdout io.Writer,
	root string,
	platform harvestpy.Platform,
	warnings int,
	gateOn bool,
) int {
	return appendHarvestBrowserDoctorRowWithRunner(
		ctx,
		stdout,
		root,
		platform,
		warnings,
		gateOn,
		obs.Runner(deps.RealRunner{}),
		doctorChromeResolver,
	)
}

func appendHarvestBrowserDoctorRowWithRunner(
	ctx context.Context,
	stdout io.Writer,
	root string,
	platform harvestpy.Platform,
	warnings int,
	gateOn bool,
	runner deps.Runner,
	chromeResolver func() string,
) int {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	if chromeResolver == nil {
		chromeResolver = func() string { return resolveChromeForDoctorWithRunner(runner) }
	}
	digest, inspectErr := harvestpy.InspectBrowser(root, platform)
	envDir := harvestpy.BrowserRuntimeRoot(root, platform)
	interpreter := filepath.Join(envDir, "project", ".venv", "bin", "python")
	script := filepath.Join(envDir, "project", "browser.py")
	fingerprint := "env=UNKNOWN"
	if inspectErr == nil {
		fingerprint = fmt.Sprintf("env=%s", browserEnvFingerprint(digest))
	}
	// S7: the gate-off row stays informational, but its states NEVER
	// collapse — never-provisioned, provisioned, and corrupt-record are
	// three different answers even while disabled.
	if !gateOn {
		switch {
		case inspectErr == nil && digest.State == "ready":
			fmt.Fprintf(stdout, "doctor: harvestpy_browser %s provisioned disabled gate=fetch.browser\n", fingerprint)
		case errors.Is(inspectErr, os.ErrNotExist):
			fmt.Fprintf(stdout, "doctor: harvestpy_browser env=NOT_PROVISIONED disabled gate=fetch.browser\n")
		case inspectErr != nil:
			fmt.Fprintf(
				stdout,
				"doctor: harvestpy_browser env=CORRUPT_RECORD disabled gate=fetch.browser error=%v\n",
				inspectErr,
			)
		default:
			fmt.Fprintf(
				stdout,
				"doctor: harvestpy_browser %s disabled gate=fetch.browser error=provision record state %q is not ready\n",
				fingerprint,
				digest.State,
			)
		}
		return warnings
	}
	if inspectErr != nil {
		if errors.Is(inspectErr, os.ErrNotExist) {
			fmt.Fprintf(
				stdout,
				"doctor: harvestpy_browser env=NOT_PROVISIONED interpreter=%s error=browser environment was never provisioned; it provisions on the first browser fetch (check uv and network access)\n",
				interpreter,
			)
		} else {
			fmt.Fprintf(stdout, "doctor: harvestpy_browser env=PROBE_FAILED error=%v\n", inspectErr)
		}
		return warnings + 1
	}
	if digest.State != "ready" {
		fmt.Fprintf(
			stdout,
			"doctor: harvestpy_browser env=PROBE_FAILED state=%s error=browser environment record is not ready\n",
			digest.State,
		)
		return warnings + 1
	}
	if _, statErr := os.Stat(interpreter); statErr != nil {
		fmt.Fprintf(
			stdout,
			"doctor: harvestpy_browser %s interpreter=%s PROBE_FAILED error=%v\n",
			fingerprint,
			interpreter,
			statErr,
		)
		return warnings + 1
	}
	// S2: verify the on-disk WORKER against the provision record before
	// anything else — browser.py carries the SSRF route guard, and a file
	// that does not match the pinned source invalidates every verdict below.
	if strings.TrimSpace(digest.SourceSHA256) == "" {
		fmt.Fprintf(
			stdout,
			"doctor: harvestpy_browser %s SOURCE_UNPINNED error=provision record predates source pinning; remove the browser environment so the next browser fetch re-provisions it\n",
			fingerprint,
		)
		return warnings + 1
	}
	if err := harvestpy.VerifySHA256(script, digest.SourceSHA256); err != nil {
		fmt.Fprintf(
			stdout,
			"doctor: harvestpy_browser %s SOURCE_MISMATCH error=on-disk browser.py does not match the pinned provision source (the SSRF route guard cannot be trusted): %v\n",
			fingerprint,
			err,
		)
		return warnings + 1
	}
	// S2: LIVE smoke — patchright importability and Chrome resolution are
	// proven NOW, on this host, exactly as a fetch would; the provision-time
	// record alone is a snapshot, and snapshots go stale silently.
	smoke, smokeErr := runDoctorBrowserSmokeWithRunner(ctx, interpreter, script, runner)
	if smokeErr != nil {
		fmt.Fprintf(
			stdout,
			"doctor: harvestpy_browser %s BROKEN_SMOKE error=live worker smoke failed: %v\n",
			fingerprint,
			smokeErr,
		)
		return warnings + 1
	}
	patchrightLive, _ := smoke["patchright"].(bool)
	if ok, _ := smoke["ok"].(bool); !ok || !patchrightLive {
		fmt.Fprintf(
			stdout,
			"doctor: harvestpy_browser %s patchright=MISSING BROKEN_SMOKE error=live smoke reports patchright did not import\n",
			fingerprint,
		)
		return warnings + 1
	}
	liveChrome, _ := smoke["chrome_path"].(string)
	chromePath, _ := digest.Imports["chrome_path"].(string)
	// Prefer the live resolution; fall back to re-verifying the recorded
	// path, then to a fresh host scan, before declaring Chrome missing.
	for _, candidate := range []string{strings.TrimSpace(liveChrome), strings.TrimSpace(chromePath)} {
		if candidate == "" || strings.ContainsRune(candidate, filepath.Separator) {
			if info, statErr := os.Stat(candidate); candidate != "" && statErr == nil && info.Mode().IsRegular() {
				fmt.Fprintf(
					stdout,
					"doctor: harvestpy_browser %s patchright=present(live smoke) chrome=%s healthy source_hash=%s\n",
					fingerprint,
					candidate,
					harvestpy.BrowserSourceState(digest),
				)
				return warnings
			}
			continue
		}
		if _, lookErr := runner.LookPath(candidate); lookErr == nil {
			fmt.Fprintf(
				stdout,
				"doctor: harvestpy_browser %s patchright=present(live smoke) chrome=%s healthy source_hash=%s\n",
				fingerprint,
				candidate,
				harvestpy.BrowserSourceState(digest),
			)
			return warnings
		}
	}
	if fallback := chromeResolver(); fallback != "" {
		fmt.Fprintf(
			stdout,
			"doctor: harvestpy_browser %s patchright=present(live smoke) chrome=%s healthy source_hash=%s\n",
			fingerprint,
			fallback,
			harvestpy.BrowserSourceState(digest),
		)
		return warnings
	}
	fmt.Fprintf(
		stdout,
		"doctor: harvestpy_browser %s patchright=present(live smoke) chrome=MISSING error=environment provisioned but no system Chrome binary resolves\n",
		fingerprint,
	)
	return warnings + 1
}

func harvestDoctorCheck(report harvestpy.CheckReport, name string, checkErr error) (bool, string) {
	if status, ok := report.Checks[name]; ok {
		return status.OK, status.Error
	}
	if report.Healthy && checkErr == nil {
		return true, ""
	}
	if checkErr != nil {
		return false, checkErr.Error()
	}
	return false, "check did not report healthy"
}

// pfmPathWarnings checks both precedence and byte identity. A copied binary
// later on PATH can become the next active binary after a shell/toolchain
// change, so checking command resolution alone is insufficient.
func pfmPathWarnings(home, pathEnvironment string) []string {
	return pfmPathWarningsWithEnv(home, pathEnvironment, paths.OSEnv{})
}

func pfmPathWarningsWithEnv(home, pathEnvironment string, env paths.Env) []string {
	canonical := filepath.Join(home, ".local", "bin", "pfm")
	canonical, _ = filepath.Abs(canonical)
	targetHome, _ := filepath.Abs(home)
	jailed := env.Get(paths.EnvHome) != "" || env.Get("PFM_DEV_FENCE") != ""
	canonicalHash, err := executableHash(canonical)
	if err != nil {
		return []string{fmt.Sprintf("pfm_canonical=%s error=%v", canonical, err)}
	}

	seen := make(map[string]bool)
	candidates := make([]string, 0)
	var warnings []string
	for _, directory := range filepath.SplitList(pathEnvironment) {
		if directory == "" {
			directory = "."
		}
		candidate, err := filepath.Abs(filepath.Join(directory, "pfm"))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("pfm_path_entry=%s error=%v", directory, err))
			continue
		}
		candidate = filepath.Clean(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if jailed {
			relative, err := filepath.Rel(targetHome, candidate)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				continue
			}
		}
		info, err := os.Stat(candidate)
		if err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("pfm_path_entry=%s error=%v", candidate, err))
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		warnings = append(warnings, "pfm_path_resolves=not-found canonical="+canonical)
		return warnings
	}
	if candidates[0] != canonical {
		warnings = append(warnings, fmt.Sprintf(
			"pfm_path_resolves=%s canonical=%s",
			candidates[0],
			canonical,
		))
	}
	for _, candidate := range candidates {
		hash, err := executableHash(candidate)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("pfm_hash_read=%s error=%v", candidate, err))
			continue
		}
		if hash != canonicalHash {
			warnings = append(warnings, fmt.Sprintf(
				"pfm_hash_mismatch=%s canonical=%s",
				candidate,
				canonical,
			))
		}
	}
	return warnings
}

func executableHash(path string) ([sha256.Size]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(content), nil
}

func liveCodexSnapshot(ctx context.Context, runtime config.Runtime, manager *kill.Manager) (gather.Snapshot, error) {
	panes, err := liveCodexPanes(ctx, runtime)
	snapshot := gather.Snapshot{Panes: panes}
	if err != nil || len(panes) == 0 {
		return snapshot, err
	}
	roots := runtime.Config.CodexHomes()
	resolver := store.NewCodexThreadResolverRoots(ctx, roots, manager.CodexPaneBound(ctx))
	snapshot.Codex, err = gather.DetectCodexThreadsInRoots(
		gather.NewProcFS(runtime.Paths.ProcRoot),
		roots,
		panes,
		resolver,
		runtime.Config.Codex.Binary,
	)
	return snapshot, err
}
