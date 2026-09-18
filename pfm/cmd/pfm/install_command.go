package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"hostops/pfm/internal/cli"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/doctor"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/professor"
	"hostops/pfm/internal/updatecheck"
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

func runInstall(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(
		installCommand,
		"usage: pfm install [--yes] [--vscode] [--skip-harvest] [--skip-engine codex] [--skip-themes] [--config-dir DIR]",
		stderr,
	)
	yes := flags.Bool("yes", false, "apply the installation")
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
	migrated, migrateCode := migrateMachineConfig(mode, stdout, stderr, runtime)
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
	options := newInstallerOptions(mode, *configDir, *skipHarvest, stdout, runtime)
	options.VSCode = *vscode
	options.InstallThemes = !*skipThemes
	options.ThemeManifestURL = professorThemeManifestURL(version)
	options.ThemeHTTPClient = installThemeHTTPClientOverride
	if skipCodex {
		options.CodexHomes = []string{}
		options.CodexYolo = map[int]bool{}
	}
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

// migrateMachineConfig moves a pre-split machine to the current layout
// (pfm.config.json + harvester.config.json, loopback port 8377 → 18377)
// BEFORE the installer reads the port it wires every client to — so client
// registrations and the restarted daemon always agree. A preview prints the
// plan and wires what the apply would.
func migrateMachineConfig(mode installer.Mode, stdout, stderr io.Writer, runtime commandRuntime) (commandRuntime, int) {
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
	if err := pfmconfig.ApplyMigration(migration); err != nil {
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
	stdout io.Writer,
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
		options.CodexYolo = make(map[int]bool, len(runtime.Config.CodexAccounts))
		options.CodexHomes = make([]string, 0, len(runtime.Config.CodexAccounts))
		for _, account := range runtime.Config.CodexAccounts {
			options.CodexHomes = append(options.CodexHomes, account.Home)
			options.CodexYolo[account.ID] = runtime.Config.EffectiveCodex(account.ID).Yolo
		}
		if configDir == "" {
			options.ConfigDirs = make([]string, 0, len(runtime.Config.Accounts))
			for _, account := range runtime.Config.Accounts {
				options.ConfigDirs = append(options.ConfigDirs, account.ConfigDir)
			}
			registries := installer.ClaudeUserRegistries(
				runtime.Paths.Home,
				runtime.Config.Accounts,
				pfmconfig.AmbientClaudeConfigDir(),
			)
			options.ClaudeRegistries = make([]string, 0, len(registries))
			options.ClaudeRegistryReasons = make(map[string]string, len(registries))
			for _, registry := range registries {
				options.ClaudeRegistries = append(options.ClaudeRegistries, registry.Path)
				options.ClaudeRegistryReasons[registry.Path] = registry.Reason
			}
		}
	}
	options.SourceRepo = resolveInstallSourceRepo(options.Home)
	return options
}

// resolveInstallSourceRepo resolves the source clone install records and the
// theme loader reads: cwd discovery (discoverSourceRepo) wins when it finds
// a clone; otherwise the source-repo marker a prior install/init recorded
// under home, the same recorded clone `pfm update` prefers via
// preferredUpdateSourceRepo. `pfm install --yes` run outside the source
// checkout (a cron job, a different cwd) must still find its own clone
// rather than falling through empty to the release manifest URL.
func resolveInstallSourceRepo(home string) string {
	if repo := professor.DiscoverSourceRepo(); repo != "" {
		return repo
	}
	if strings.TrimSpace(home) == "" {
		return ""
	}
	recorded, err := installer.ReadSourceRepoMarker(home)
	if err != nil {
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
