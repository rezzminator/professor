package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cli"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/updatecheck"
)

// installHarvestProvisioner is nil in production and resolves to the real
// pinned adapter. The command-package TestMain replaces it with a no-network
// fake so existing CLI wiring tests never download the conversion lock.
var (
	installHarvestProvisionerOverride installer.HarvestProvisioner
	installThemeHTTPClientOverride    *http.Client
)

var runInstaller = installer.Run

// checkInstallSpace is the applying run's space preflight; a test swaps it.
var checkInstallSpace = installer.CheckInstallSpace

func installHarvestProvisioner() installer.HarvestProvisioner {
	if installHarvestProvisionerOverride != nil {
		return installHarvestProvisionerOverride
	}
	return installer.NewHarvestProvisioner()
}

func runInstall(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(
		installCommand,
		"usage: pfm install [--yes] [--rollback ID [--force]] [--vscode] [--skip-harvest] [--skip-engine codex] [--skip-themes] [--config-dir DIR]",
		stderr,
	)
	yes := flags.Bool("yes", false, "apply the installation")
	rollback := flags.String("rollback", "", "replay a layout journal backwards")
	force := flags.Bool("force", false, "with --rollback: overwrite destinations changed since the install")
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
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	rollbackSet := false
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == "rollback" {
			rollbackSet = true
		}
	})
	if *force && !rollbackSet {
		flags.Usage()
		return 2
	}
	if rollbackSet {
		other := false
		flags.Visit(func(flag *flag.Flag) {
			if flag.Name != "rollback" && flag.Name != "force" {
				other = true
			}
		})
		if other || *rollback == "" {
			flags.Usage()
			return 2
		}
		return runInstallRollback(*rollback, *force, stdout, stderr, runtimes)
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
		if mode == installer.ModeApply {
			fmt.Fprintf(stderr, "pfm install: %s\n", refusal)
			return 1
		}
		fmt.Fprintf(stdout, "  skip    %s\n", refusal)
	}
	layoutEnv, err := installer.NewInstallLayoutEnv(runtime, paths.OSEnv{}, professor.DiscoverSourceRepo())
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: layout environment: %v\n", err)
		return 1
	}
	journal := installer.NewJournal(context.Background(), layoutEnv)
	if mode == installer.ModeApply {
		// The run's one journal line is its last stdout line, on every return
		// once anything was recorded — a failure included.
		defer func() {
			if dir := journal.Dir(); dir != "" {
				fmt.Fprintln(stdout, "install journal: "+dir)
			}
		}()
	}
	// withFlags carries the command's flags into an installer run: the apply
	// and the space preflight's planning pass see the same install.
	withFlags := func(options installer.Options) installer.Options {
		options.VSCode = *vscode
		options.InstallThemes = !*skipThemes
		options.ThemeManifestURL = professorThemeManifestURL(version)
		options.ThemeHTTPClient = installThemeHTTPClientOverride
		if skipCodex {
			options.CodexHomes = []string{}
		}
		return options
	}
	layoutFindings := installer.ClassifyLayout(layoutEnv)
	if mode == installer.ModeApply {
		planOptions := func(runtime commandRuntime) installer.Options {
			return withFlags(
				newInstallerOptions(installer.ModeDryRun, *configDir, *skipHarvest, io.Discard, io.Discard, runtime),
			)
		}
		if code := installSpacePreflight(layoutEnv, layoutFindings, runtime, planOptions, stderr); code != 0 {
			return code
		}
	}
	journalDir, err := installer.ApplyLayout(
		context.Background(), layoutEnv, journal, mode == installer.ModeApply, stdout,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: %v\n", err)
		return 1
	}
	if mode == installer.ModeApply && journalDir != "" {
		for _, finding := range layoutFindings {
			if (finding.Row != "state-db" && finding.Row != "cache-db") || finding.Source == "" {
				continue
			}
			if _, sourceErr := os.Lstat(finding.Source); !os.IsNotExist(sourceErr) {
				continue
			}
			if _, targetErr := os.Stat(finding.Path); targetErr != nil {
				continue
			}
			if migrateErr := migrateInstalledLayoutDatabases(
				context.Background(),
				layoutEnv.StateDB,
				layoutEnv.CacheDB,
			); migrateErr != nil {
				fmt.Fprintf(stderr, "pfm install: migrate moved databases: %v\n", migrateErr)
				return 1
			}
			// The migration rewrote the moved state database and may have
			// written its pre-migration backup: this install's own change,
			// never drift for a rollback.
			backups, globErr := filepath.Glob(layoutEnv.StateDB + ".bak-before-v*")
			if globErr != nil {
				fmt.Fprintf(stderr, "pfm install: list state database backups: %v\n", globErr)
				return 1
			}
			if err := journal.Refingerprint(append([]string{layoutEnv.StateDB}, backups...)...); err != nil {
				fmt.Fprintf(stderr, "pfm install: fingerprint migrated state database: %v\n", err)
				return 1
			}
			break
		}
	}
	installConfig, configErr := layoutEnv.InstallConfig(runtime, mode == installer.ModeApply)
	if configErr != nil {
		fmt.Fprintf(stderr, "pfm install: %v\n", configErr)
		return 1
	}
	runtime.Config = installConfig
	migrated, migrateCode := migrateMachineConfig(mode, journal, stdout, stderr, runtime)
	if migrateCode != 0 {
		return migrateCode
	}
	runtime = migrated
	entries := deps.Registry(deps.Options{
		Home: runtime.Paths.Home, ClaudeBinary: runtime.Config.Claude.Binary, CodexBinary: runtime.Config.Codex.Binary,
	})
	_, preflight, _ := doctor.PrintDependencies(
		context.Background(),
		stdout,
		runtime.Paths.Home,
		entries,
		deps.ProbeOptions{
			SkipHarvest:  *skipHarvest,
			SkipEngines:  map[pfmengine.ID]bool{pfmengine.Codex: skipCodex},
			Provisioning: true,
			Runner:       obs.Runner(deps.RealRunner{}),
		},
	)
	if preflight != 0 && mode == installer.ModeApply {
		fmt.Fprintln(stderr, "pfm install: required dependency preflight failed")
		return 1
	}
	options := withFlags(newInstallerOptions(mode, *configDir, *skipHarvest, stdout, stderr, runtime))
	options.Journal = journal
	code := runInstallerCommand(installCommand, options, stderr)
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

// installSpacePreflight plans the run — the installer in dry-run with a fresh
// journal, plus the harvester root a re-provision journals — and refuses the
// apply when a filesystem cannot take the journal copies and cross-filesystem
// moves (installer.CheckInstallSpace).
func installSpacePreflight(
	layoutEnv installer.LayoutEnv,
	findings []installer.LayoutFinding,
	runtime commandRuntime,
	planOptions func(commandRuntime) installer.Options,
	stderr io.Writer,
) int {
	ctx := context.Background()
	planConfig, err := layoutEnv.InstallConfig(runtime, false)
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: space preflight: plan install writes: %v\n", err)
		return 1
	}
	runtime.Config = planConfig
	options := planOptions(runtime)
	plan := installer.NewJournal(ctx, layoutEnv)
	options.Journal = plan
	if _, err := runInstaller(ctx, options); err != nil {
		fmt.Fprintf(stderr, "pfm install: space preflight: plan install writes: %v\n", err)
		return 1
	}
	planned := append(plan.Planned(), installer.PlanHarvestJournal(ctx, options)...)
	if err := checkInstallSpace(layoutEnv, findings, planned); err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			fmt.Fprintf(stderr, "pfm install: %s\n", line)
		}
		return 1
	}
	return 0
}

func migrateInstalledLayoutDatabases(ctx context.Context, statePath, cachePath string) (returnErr error) {
	resolved, err := pfmconfig.ResolvePaths()
	if err != nil {
		return fmt.Errorf("resolve moved database paths: %w", err)
	}
	if resolved.StateDB != statePath || resolved.CacheDB != cachePath {
		return fmt.Errorf("moved database paths differ from resolved paths: state %s != %s; cache %s != %s",
			statePath, resolved.StateDB, cachePath, resolved.CacheDB)
	}
	database, err := store.OpenContext(ctx)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, database.Close()) }()
	if err := database.SharedDegraded(); err != nil {
		return fmt.Errorf("shared state: %w", err)
	}
	return nil
}

// migrateMachineConfig moves a pre-split machine to the current layout
// (pfm.config.json + harvester.config.json, loopback port 8377 → 18377)
// BEFORE the installer reads the port it wires every client to — so client
// registrations and the restarted daemon always agree. A preview prints the
// plan and wires what the apply would.
func migrateMachineConfig(
	mode installer.Mode,
	journal *installer.Journal,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) (commandRuntime, int) {
	migration, err := pfmconfig.PlanMigration(runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: plan config migration: %v\n", err)
		return runtime, 1
	}
	if migration.Empty() {
		return runtime, 0
	}
	fmt.Fprintln(stdout, "config migration (pre-split layout):")
	for _, step := range migration.Steps() {
		fmt.Fprintf(stdout, "  change  %s\n", step)
	}
	if mode != installer.ModeApply {
		runtime.Config = migration.Preview(runtime.Config)
		return runtime, 0
	}
	apply := func() error { return pfmconfig.ApplyMigration(migration) }
	if err := journal.Write(configMigrationPaths(migration), apply); err != nil {
		fmt.Fprintf(stderr, "pfm install: apply config migration: %v\n", err)
		return runtime, 1
	}
	reloaded, err := pfmconfig.LoadRuntime(migration.Path)
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: reload migrated config %s: %v\n", migration.Path, err)
		return runtime, 1
	}
	return reloaded, 0
}

// configMigrationPaths names every file pfmconfig.ApplyMigration writes,
// renames or removes: the config, its harvester sibling, the pre-split file
// and the parked copy it becomes.
func configMigrationPaths(migration pfmconfig.Migration) []string {
	changed := []string{migration.Path, pfmconfig.HarvesterPath(migration.Path)}
	for _, legacy := range []string{migration.LegacyPath, migration.StrayLegacyPath} {
		if legacy != "" {
			changed = append(changed, legacy, filepath.Join(filepath.Dir(legacy), pfmconfig.LegacyBackupName))
		}
	}
	return changed
}

func professorThemeManifestURL(currentVersion string) string {
	reference := strings.TrimSpace(currentVersion)
	if reference == "" || reference == pfmconfig.DevelopmentVersion {
		reference = "main"
	}
	return "https://raw.githubusercontent.com/" + updatecheck.ProfessorRepo + "/" + reference + "/templates/themes/sources.json"
}

func newInstallerOptions(
	mode installer.Mode,
	configDir string,
	skipHarvest bool,
	stdout, stderr io.Writer,
	runtimes ...commandRuntime,
) installer.Options {
	options := installer.Options{
		Mode:               mode,
		ConfigDir:          configDir,
		Stdout:             stdout,
		ProvisionHarvest:   !skipHarvest,
		HarvestProvisioner: installHarvestProvisioner(),
	}
	if len(runtimes) != 0 {
		runtime := runtimes[0]
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
		options.CodexBinary = runtime.Config.Codex.Binary
		if options.CodexBinary == "" {
			options.CodexBinary = pfmengine.MustLookup(pfmengine.Codex).Binary
		}
		options.NameSyncInterval = runtime.Config.NameSync.Interval
		options.CodexHomes = make([]string, 0, len(runtime.Config.CodexAccounts))
		for _, account := range runtime.Config.CodexAccounts {
			options.CodexHomes = append(options.CodexHomes, account.Home)
		}
		if configDir == "" {
			options.ConfigDirs = make([]string, 0, len(runtime.Config.Accounts))
			for _, account := range runtime.Config.Accounts {
				options.ConfigDirs = append(options.ConfigDirs, account.ConfigDir)
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
	if errors.Is(err, installer.ErrNameSyncRunning) {
		fmt.Fprintf(
			stderr,
			"pfm %s: the pfm name-sync service is running; wait for it to finish or run `systemctl --user stop pfm-name-sync.service`, then retry\n",
			command,
		)
		return 97
	}
	if errors.Is(err, installer.ErrLaunchAgentRunning) {
		fmt.Fprintf(
			stderr,
			"pfm %s: the pfm name-sync launch agent is running; wait for it to finish or `launchctl bootout gui/$(id -u)/com.professor.pfm.name-sync` first\n",
			command,
		)
		return 97
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm %s: %v\n", command, err)
		return 1
	}
	return 0
}
