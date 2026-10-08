package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/hostcheck"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

// installHarvestProvisioner is nil in production and resolves to the real
// pinned adapter. The command-package TestMain replaces it with a no-network
// fake so existing CLI wiring tests never download the conversion lock.
var (
	installHarvestProvisionerOverride installer.HarvestProvisioner
	installThemeHTTPClientOverride    *http.Client
)

var runInstaller = installer.Run

func installHarvestProvisioner() installer.HarvestProvisioner {
	if installHarvestProvisionerOverride != nil {
		return installHarvestProvisionerOverride
	}
	return installer.NewHarvestProvisioner()
}

func runInstall(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) (code int) {
	flags := cli.NewFlagSet(
		installCommand,
		"usage: pfm install [--yes] [--check [--plan]] [--vscode] [--skip-harvest] [--skip-engine codex] [--skip-themes] [--config-dir DIR]",
		stderr,
	)
	yes := flags.Bool("yes", false, "apply the installation")
	check := flags.Bool(
		"check",
		false,
		"answer whether --yes would refuse before any change (exit 4: a blocking host check or a running name-sync job)",
	)
	plan := flags.Bool("plan", false, "with --check: print every host check's fix in the order to apply them")
	vscode := flags.Bool(
		"vscode",
		false,
		"install the Professor VS Code extension and make the PFM terminal the default",
	)
	skipHarvest := flags.Bool("skip-harvest", false, "skip harvestpy provisioning")
	skipEngine := flags.String("skip-engine", "", "skip one optional engine (supported: codex)")
	skipThemes := flags.Bool("skip-themes", false, "skip Claude Code themes, source-fetched and bundled")
	configDir := flags.String("config-dir", "", "target config directory instead of ~/.claude")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if *plan && !*check {
		fmt.Fprintln(stderr, "pfm install: --plan needs --check: run pfm install --check --plan")
		return 2
	}
	if flags.NArg() != 0 || *check && *yes {
		flags.Usage()
		return 2
	}
	skipCodex := false
	if value := strings.TrimSpace(*skipEngine); value != "" {
		id, err := pfmengine.Parse(value)
		if err != nil || id != pfmengine.Codex {
			fmt.Fprintf(stderr, "pfm install: --skip-engine supports only codex, got %q\n", value)
			return 2
		}
		skipCodex = true
	}
	mode := installer.ModeDryRun
	if *yes {
		mode = installer.ModeApply
	}
	runtime, runtimeErr := pfmconfig.OptionalRuntime(runtimes)
	if runtimeErr != nil {
		fmt.Fprintf(stderr, "pfm install: resolve dependency config: %v\n", runtimeErr)
		return 1
	}
	// An explicit --config naming a file that does not exist quietly loads
	// as defaults (config.go); converging host wiring on that would boot out
	// and delete every MCP service the missing file actually enabled (issue
	// #24 finding 3/4). Apply refuses; a preview names the skip and continues.
	if runtime.ConfigExplicit && !runtime.Config.Exists {
		refusal := fmt.Sprintf(
			"--config %s does not exist; refusing to converge host wiring on defaults (a missing explicit config would disable every MCP service it names)",
			runtime.Config.Path,
		)
		if mode == installer.ModeApply || *check {
			fmt.Fprintf(stderr, "pfm install: %s\n", refusal)
			return 1
		}
		fmt.Fprintf(stdout, "  skip    %s\n", refusal)
	}
	env := hostcheck.EnvFor(runtime, clock.Real.Now())
	// --plan prints and judges every row itself; the gate below then sees
	// none, so a plan with only WARN rows goes on to the remaining checks.
	var rows []hostcheck.Row
	if !*plan {
		rows = hostcheck.RunAll(env)
	} else if code, refusal := hostcheck.RunPlan(env).Print(stdout); code != 0 {
		fmt.Fprintln(stderr, "pfm install: "+refusal)
		return code
	}
	if blocking := hostcheck.Count(rows, hostcheck.Block); blocking != 0 {
		for _, row := range rows {
			if row.Severity == hostcheck.Block {
				fmt.Fprint(stderr, row.Render("pfm install: "))
			}
		}
		fmt.Fprintf(stderr, "pfm install: %d blocking — run pfm doctor for the fixes\n", blocking)
		return 4
	}
	if warnings := hostcheck.Count(rows, hostcheck.Warn); warnings != 0 {
		fmt.Fprintf(stdout, "pfm install: %d warnings — run pfm doctor to see them\n", warnings)
	}
	withFlags := func(options installer.Options) installer.Options {
		options.VSCode = *vscode
		options.InstallThemes = !*skipThemes
		options.ThemeManifestURL = installer.ThemeManifestURL(version)
		options.ThemeHTTPClient = installThemeHTTPClientOverride
		if skipCodex {
			options.CodexHomes = []string{}
		}
		return options
	}
	// printDependencies is the required dependency preflight: it prints the
	// table and returns its failure count. Provisioning only marks the
	// harvester rows "provisioned by install"; every probe is read-only.
	printDependencies := func(w io.Writer, runtime commandRuntime) int {
		entries := deps.Registry(deps.Options{
			Home:         runtime.Paths.Home,
			ClaudeBinary: runtime.Config.Claude.Binary,
			CodexBinary:  runtime.Config.Codex.Binary,
		})
		probe := deps.ProbeOptions{
			SkipHarvest:  *skipHarvest,
			SkipEngines:  map[pfmengine.ID]bool{pfmengine.Codex: skipCodex},
			Provisioning: true,
			Runner:       obs.Runner(deps.RealRunner{}),
		}
		_, failures, _ := doctor.PrintDependencies(context.Background(), w, runtime.Paths.Home, entries, probe)
		return failures
	}
	if mode == installer.ModeApply || *check {
		if printDependencies(stdout, runtime) != 0 {
			fmt.Fprintln(stderr, "pfm install: required dependency preflight failed")
			return 1
		}
	}
	if *check {
		unprobed, err := installer.CheckScheduler(context.Background(), nil)
		if err != nil {
			fmt.Fprintln(stderr, installer.SchedulerRefusal(installCommand, err))
			return 4
		}
		fmt.Fprintln(stdout, "install check: ok — pfm install --yes would pass its pre-change checks")
		if unprobed != "" {
			fmt.Fprintln(stdout, "  skip    "+unprobed)
		}
		return 0
	}
	installConfig, seeded, configErr := installer.InstallConfig(
		runtime,
		paths.OSEnv{},
		professor.DiscoverSourceRepo(),
	)
	if configErr != nil {
		fmt.Fprintf(stderr, "pfm install: seed config: %v\n", configErr)
		return 1
	}
	runtime = runtime.WithConfig(installConfig, paths.OSEnv{})
	defer pfmconfig.UseConfigPath(runtime.Config.Path, runtime.Config.State)()
	// An apply ran the dependency preflight before its first write.
	preflight := 0
	if mode != installer.ModeApply {
		preflight = printDependencies(stdout, runtime)
	}
	options := withFlags(newInstallerOptions(mode, *configDir, *skipHarvest, stdout, stderr, runtime))
	options.ConfigSeed = seeded
	options.ConfigSeedContent = installConfig.SeedContent
	code = runInstallerCommand(installCommand, options, stderr)
	if code == 0 && mode == installer.ModeDryRun {
		if preflight != 0 {
			fmt.Fprintln(
				stderr,
				"pfm install: required dependency preflight failed — the preview above is read-only; fix the dependencies it names before applying",
			)
			return 1
		}
		confirmation := "if you agree, run again: pfm install --yes"
		if *vscode {
			confirmation += " --vscode"
		}
		if *skipHarvest {
			confirmation += " --skip-harvest"
		}
		if skipCodex {
			confirmation += " --skip-engine codex"
		}
		if *skipThemes {
			confirmation += " --skip-themes"
		}
		fmt.Fprintln(stdout, confirmation)
	}
	return code
}

func newInstallerOptions(
	mode installer.Mode,
	configDir string,
	skipHarvest bool,
	stdout, stderr io.Writer,
	runtimes ...commandRuntime,
) installer.Options {
	options := installer.Options{
		Mode:                mode,
		ConfigDir:           configDir,
		Stdout:              stdout,
		ProvisionHarvest:    !skipHarvest,
		SkillSourcesOffline: paths.SkillSourcesOffline(),
		ThemesOffline:       paths.ThemesOffline(),
		HarvestProvisioner:  installHarvestProvisioner(),
	}
	if len(runtimes) != 0 {
		runtime := runtimes[0]
		options.ManagedSettingsDir = runtime.Paths.ManagedSettingsDir
		options.CleanupPeriodDays = runtime.Config.Claude.CleanupPeriodDays
		options.RequireManagedCleanup = runtime.Config.Claude.RequireManagedCleanup
		options.Home = runtime.Paths.Home
		options.StateDB = runtime.Paths.StateDB
		options.MCPEnabled = make(map[string]bool, len(runtime.Config.MCPServers))
		for name, server := range runtime.Config.MCPServers {
			options.MCPEnabled[name] = server.Enabled
		}
		options.MCPPort = runtime.Config.MCP.HTTP.Port
		options.MCPConfigPath = runtime.Config.Path
		options.OpenCodeConfigPath = installer.OpenCodeConfigPath(runtime.Paths.Home)
		options.ClaudeBinary = runtime.Config.Claude.Binary
		options.Version = runtime.Version
		options.ClaudePluginCheckoutRoot = runtime.Config.Claude.PluginCheckoutRoot
		options.CodexBinary = runtime.Config.Codex.Binary
		if options.CodexBinary == "" {
			options.CodexBinary = pfmengine.MustLookup(pfmengine.Codex).Binary
		}
		options.NameSyncInterval = runtime.Config.NameSync.Interval
		options.CodexHomes = make([]string, 0, len(runtime.Config.CodexAccounts))
		for _, account := range runtime.Config.CodexAccounts {
			options.CodexHomes = append(options.CodexHomes, account.Home)
		}
		options.ClaudeRosterHost = len(runtime.Config.Accounts) > 0
		for _, account := range runtime.Config.Accounts {
			options.RosterConfigDirs = append(options.RosterConfigDirs, account.ConfigDir)
		}
		if configDir == "" {
			options.ClaudeAccounts = runtime.Config.Accounts
			if len(runtime.Config.Accounts) > 0 {
				id, err := fleet.PrimaryAccount(runtime.Paths, runtime.Config)
				if err != nil {
					id = runtime.Config.ImplicitAccount()
					fmt.Fprintf(stdout, "  skip    primary account unreadable (%v); using account %d\n", err, id)
				}
				account, _ := runtime.Config.AccountByID(id)
				options.PrimaryConfigDir = account.ConfigDir
			}
		}
	}
	options.SourceRepo = resolveInstallSourceRepo(options.Home, stderr)
	return options
}

// resolveInstallSourceRepo resolves the source clone install records and the
// theme loader reads: cwd discovery (discoverSourceRepo) wins when it finds
// a clone; otherwise the source-repo marker a prior install/init recorded
// under home, the same recorded clone `pfm update` prefers via
// preferredUpdateSourceRepo. `pfm install --yes` run outside the source
// checkout (a cron job, a different cwd) must still find its own clone
// rather than falling through empty to the release manifest URL.
//
// Both misses end in the same fallback, and only one of them is ordinary: no
// marker at all is a first install and stays silent, while a marker naming a
// clone that has moved, vanished or become unreadable is named on stderr
// first — install would otherwise fetch from GitHub without a word, an error
// rendered as absence. `pfm init` (init_command.go) reports the same failure.
func resolveInstallSourceRepo(home string, stderr io.Writer) string {
	if repo := professor.DiscoverSourceRepo(); repo != "" {
		return repo
	}
	if strings.TrimSpace(home) == "" {
		return ""
	}
	// No marker at all is a first install and stays silent; every other miss
	// (a recorded clone that moved, vanished or became unreadable) is named.
	recorded, err := paths.ReadSourceRepoMarker(home)
	if errors.Is(err, paths.ErrNoSourceRepoMarker) {
		return ""
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: %v; falling back to the release manifest\n", err)
		return ""
	}
	return recorded
}

func runInstallerCommand(command string, options installer.Options, stderr io.Writer) int {
	_, err := runInstaller(context.Background(), options)
	if refusal := installer.SchedulerRefusal(command, err); refusal != "" {
		fmt.Fprintln(stderr, refusal)
		return 97
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm %s: %v\n", command, err)
		return 1
	}
	return 0
}
