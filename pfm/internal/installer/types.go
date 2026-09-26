// Package installer owns the host-level pfm command, hook, launcher, and unit
// wiring. Its assets are embedded so one pfm binary is a complete installer.
package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// ErrNameSyncRunning refuses a mutating install while the Linux name-sync
// service is executing. A reachable but idle user manager is safe, and a dry
// run never needs this gate because it writes nothing.
var ErrNameSyncRunning = errors.New("the pfm name-sync service is running")

// ErrLaunchAgentRunning is the macOS half of the same narrow refusal: a
// mutating install must not rewrite the agent and its binary mid-execution.
var ErrLaunchAgentRunning = errors.New("the pfm name-sync launch agent is running")

type Mode uint8

const (
	ModeDryRun Mode = iota
	ModeApply
	ModeUninstall
)

const (
	configTypeKey     = "type"
	configCommandKey  = "command"
	configArgsKey     = "args"
	configEnvKey      = "env"
	commandType       = "command"
	legacyFleetBinary = "cc-fleet"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) error
}

// HarvestProvisioner is the narrow seam between host wiring and the pinned
// Python runtime. The real implementation delegates to harvestpy; installer
// tests inject a fake so they never download the conversion lock.
type HarvestProvisioner interface {
	Plan(harvestpy.Platform) (harvestpy.InstallPlan, error)
	Provision(context.Context, harvestpy.ProvisionOptions) (harvestpy.ProvisionResult, error)
	Check(context.Context, string, harvestpy.Platform) (harvestpy.CheckReport, error)
}

// OutputRunner is the optional half of CommandRunner for probes that need to
// READ a manager's answer rather than just its exit status. launchctl reports a
// job's state in its output and exits zero either way, so the launch-agent gate
// cannot be built on exit codes alone. A runner that does not implement it
// cannot be probed, and the installer says so rather than assuming safety.
type OutputRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type Options struct {
	Mode      Mode
	Home      string
	ConfigDir string
	// ConfigDirs is the config-driven settings fanout. A nil value retains
	// the historical discovery of existing .cc account settings for callers
	// that construct Options directly.
	ConfigDirs []string
	// ClaudeRegistries carries actual user-scope paths, including implicit accounts.
	// Nil derives legacy paths from ConfigDirs; empty means no Claude clients.
	ClaudeRegistries []string
	// ClaudeRegistryReasons explains, for a path also present in
	// ClaudeRegistries, why that file is a registry a pfm-launched Claude
	// reads (see ClaudeUserRegistries). A path with no entry writes with the
	// historical unreasoned message; callers that populate ClaudeRegistries
	// from ClaudeUserRegistries populate this too.
	ClaudeRegistryReasons map[string]string
	// CodexHomes is the config-driven hooks.json fanout. A nil value retains
	// the historical single ~/.codex target for direct legacy callers; an
	// explicitly empty roster installs no Codex hook.
	CodexHomes []string
	// CodexBinary enables native hook trust registration for command callers.
	CodexBinary string
	Clock       clock.Clock
	Env         paths.Env
	// SourceRepo is the clone whose templates and binary are being installed.
	// Empty preserves an existing marker when install is invoked elsewhere.
	SourceRepo string
	Now        func() time.Time
	// Sleep paces the installer's own waits — today the poll that lets a
	// launchd teardown finish before the job is bootstrapped again. Tests
	// supply a no-op so a bounded wait costs them nothing.
	Sleep  func(time.Duration)
	Stdout io.Writer
	Runner CommandRunner
	// ProcessRunner owns installer commands whose stdout/stderr and exit code
	// are part of the result (for example rumdl and uv). Runner remains the
	// compatibility seam for simple command/status probes.
	ProcessRunner deps.Runner

	MCPEnabled    map[string]bool
	MCPPort       int
	MCPConfigPath string
	// OpenCodeConfigPath is the machine-scope JSONC registry OpenCode reads.
	// Command callers always resolve it from the effective home; direct legacy
	// callers may leave it empty to opt out of OpenCode wiring.
	OpenCodeConfigPath string
	ClaudeBinary       string
	CodexYolo          map[int]bool
	// NameSyncInterval is the machine config's nameSync.interval. It renders
	// into BOTH schedulers — the launchd job's StartInterval and the systemd
	// timer's OnUnitInactiveSec — from this ONE value, so a host that switches
	// managers cannot find two different polls. Zero means the shipped
	// default (config.DefaultNameSyncInterval).
	NameSyncInterval time.Duration
	// VSCode explicitly opts the first install into installing the Professor
	// VS Code extension (Professor's assistant in VS Code) and making the PFM
	// settings terminal profile the platform default. Once written, the
	// ownership ledger keeps later ordinary installs and updates reconciled
	// without requiring the flag again.
	VSCode bool

	// Test seams for platform/path discovery. Production callers leave these
	// empty so the installer discovers the current host's VS Code settings.
	vscodePlatform      string
	vscodeSettingsPaths []string
	// vscodeExtensionRoots overrides the VS Code product roots the installer
	// checks for an extensions/ directory to link into. Nil means discover
	// the real ~/.vscode* family under Options.Home; tests set it so they
	// never touch a real home.
	vscodeExtensionRoots []string

	// ProvisionHarvest makes install/uninstall own the pinned conversion
	// environment. The command sets this for real user actions; existing
	// installer unit tests leave it false and inject no network-capable worker.
	ProvisionHarvest   bool
	HarvestProvisioner HarvestProvisioner
	HarvestPlatform    harvestpy.Platform
	HarvestOffline     bool

	// ProcRoot is the process table pruneClaudeVersions reads to tell a
	// version a live chat is executing from one it is safe to remove. Empty
	// resolves to PFM_PROC_ROOT-or-/proc in normalizeInstallerOptions, same as the rest of
	// the fleet; jail tests set it directly so the probe never touches a
	// real /proc.
	ProcRoot string

	// InstallThemes enables the optional Claude Code themes, source-fetched and bundled.
	// Command callers set it by default; unit callers opt in explicitly so a
	// test can never acquire network access by accident.
	InstallThemes bool
	// ThemeManifestURL is the release-matched fallback used when SourceRepo is
	// unavailable (for example, the checksum-verified binary install path).
	ThemeManifestURL string
	ThemeHTTPClient  *http.Client

	// launchGateUnprobed records that the launch-agent gate could not ask its
	// question. It is set by Run, never by a caller.
	launchGateUnprobed bool
	// nameSyncGateUnprobed is the systemd twin: it records that the Linux
	// name-sync gate's systemctl probe never ran at all (as opposed to
	// running and reporting the service inactive). It is set by Run, never by
	// a caller.
	nameSyncGateUnprobed bool
}

type Report struct {
	Changed int
	OK      int
	Skipped int
}

type execCommandRunner struct{}

type commandExitError struct {
	name string
	code int
	text string
}

func (err commandExitError) Error() string {
	return fmt.Sprintf("%s exited %d: %s", err.name, err.code, err.text)
}

func (err commandExitError) ExitCode() int { return err.code }

func (installer *engine) env() paths.Env {
	if installer.options.Env != nil {
		return installer.options.Env
	}
	return paths.OSEnv{}
}

func (installer *engine) now() time.Time {
	if installer.options.Now != nil {
		return installer.options.Now()
	}
	if installer.options.Clock != nil {
		return installer.options.Clock.Now()
	}
	return clock.Real.Now()
}

func (installer *engine) processRunner() deps.Runner {
	if installer.options.ProcessRunner != nil {
		return installer.options.ProcessRunner
	}
	return obs.Runner(deps.RealRunner{})
}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	result, err := obs.Runner(deps.RealRunner{}).Run(ctx, append([]string{name}, args...), deps.RunOptions{})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return commandExitError{name: name, code: result.ExitCode, text: string(result.Stderr)}
	}
	return nil
}

func (execCommandRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	result, err := obs.Runner(deps.RealRunner{}).Run(ctx, append([]string{name}, args...), deps.RunOptions{})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return result.Stdout, commandExitError{name: name, code: result.ExitCode, text: string(result.Stderr)}
	}
	return result.Stdout, nil
}

func normalizeInstallerOptions(options Options) (Options, error) {
	if options.Clock == nil {
		options.Clock = clock.Real
	}
	if options.Env == nil {
		options.Env = paths.OSEnv{}
	}
	if options.Home == "" {
		var err error
		options.Home, err = options.Env.Home()
		if err != nil {
			return options, err
		}
	}
	if options.ConfigDir == "" {
		options.ConfigDir = options.Home + "/.claude"
	}
	if options.ProcRoot == "" {
		options.ProcRoot = options.Env.Get(paths.EnvProcRoot)
		if options.ProcRoot == "" {
			options.ProcRoot = "/proc"
		}
	}
	if options.MCPPort == 0 {
		options.MCPPort = pfmconfig.DefaultMCPPort
	}
	if options.CodexYolo == nil {
		options.CodexYolo = map[int]bool{1: true, 2: true, 3: true}
	}
	if options.Now == nil {
		options.Now = options.Clock.Now
	}
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Runner == nil {
		options.Runner = execCommandRunner{}
	}
	if options.ProcessRunner == nil {
		options.ProcessRunner = obs.Runner(deps.RealRunner{})
	}
	if options.ProvisionHarvest && options.HarvestProvisioner == nil {
		options.HarvestProvisioner = NewHarvestProvisioner()
	}
	return options, nil
}

// harvestModelStager is the optional OCR-model half of a HarvestProvisioner:
// the production adapter stages the models; test doubles need not.
type harvestModelStager interface {
	StageOCRModels(context.Context, harvestpy.OCRStageOptions) (harvestpy.OCRStaging, error)
}

func (pinnedHarvestProvisioner) StageOCRModels(
	ctx context.Context,
	options harvestpy.OCRStageOptions,
) (harvestpy.OCRStaging, error) {
	return harvestpy.StageOCRModels(ctx, options)
}

// stageHarvestModels stages the OCR models after the environment is healthy:
// it names the download and its size before it starts, answers a warm cache
// without a download, and names an offline install that could not stage.
func (installer *engine) stageHarvestModels(
	ctx context.Context,
	provider HarvestProvisioner,
	root string,
	platform harvestpy.Platform,
) error {
	stager, ok := provider.(harvestModelStager)
	if !ok {
		return nil
	}
	staging, err := stager.StageOCRModels(ctx, harvestpy.OCRStageOptions{
		Root: root, Platform: platform, Offline: installer.options.HarvestOffline,
		Announce: func(message string) { installer.say("harvestpy OCR models: %s", message) },
	})
	if errors.Is(err, harvestpy.ErrOCRModelsOffline) {
		installer.say(
			"harvestpy OCR models: NOT staged — %v; scanned PDFs fail by name until `pfm install` runs with network",
			err,
		)
		return nil
	}
	if err != nil {
		return fmt.Errorf("harvestpy OCR model staging: %w", err)
	}
	if staging.AlreadyStaged {
		installer.ok("harvestpy OCR models already staged in " + staging.ModelRoot + " (no download)")
		return nil
	}
	installer.ok(fmt.Sprintf("harvestpy OCR models staged in %s (%d bytes on disk); Hebrew: %s",
		staging.ModelRoot, staging.Bytes, staging.Hebrew))
	for set, reason := range staging.Skipped {
		installer.say("harvestpy OCR models: %s not staged — %s", set, reason)
	}
	return nil
}
