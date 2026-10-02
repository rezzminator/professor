package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
)

// rumdlPinnedVersion is the rumdl release pfm provisions via `uv tool
// install`, referenced by both the dry-run plan text and the real install
// args below. It is the same release the shipped .rumdl.toml policy
// (MD060 compact style, per-file-ignores) was validated against — the
// deps registry's "rumdl" entry carries the matching MinVersion.
const rumdlPinnedVersion = "0.2.73"

// maxRumdlFailureOutput bounds how much of a failed `uv tool install`
// invocation's combined output reaches the install log line — enough to
// diagnose a real failure, never enough to flood it.
const maxRumdlFailureOutput = 4096

// rumdlUserConfig is the host-wide rumdl user config pfm install writes when
// none exists. rumdl reads it only when a run finds no project config, so a
// project .rumdl.toml (and its cache-dir) still wins entirely.
const rumdlUserConfig = `# Written by pfm install: rumdl runs that find no project .rumdl.toml cache nothing,
# so no stray .rumdl_cache appears in the working directory.
[global]
cache = false
`

// installMarkdownTool provisions rumdl, the markdown linter/formatter the
// framework's prompts and docs are checked against, then its user config. It
// is a soft dependency: only a journal failure may fail `pfm install`.
func (installer *engine) installMarkdownTool(ctx context.Context) error {
	if err := installer.provisionRumdl(ctx); err != nil {
		return err
	}
	return installer.ensureRumdlUserConfig()
}

// provisionRumdl installs the pinned rumdl unless one at or above the pin is
// present (mirrors installHarvest's dry-run gate and ok/skip/say reporting
// idiom, but every terminal state but a journal failure returns nil).
func (installer *engine) provisionRumdl(ctx context.Context) error {
	processRunner := installer.processRunner()
	platform := installer.harvestPlatform()
	provisionedUV := filepath.Join(harvestpy.RuntimeRoot(harvestPythonRoot(installer.options.Home), platform), "uv")

	if path, err := processRunner.LookPath("rumdl"); err == nil {
		if version, versionErr := rumdlVersionWithRunner(
			ctx,
			path,
			processRunner,
		); versionErr == nil &&
			deps.AtLeast(version, rumdlPinnedVersion) {
			installer.ok(fmt.Sprintf("rumdl already present (%s)", version))
			return nil
		}
	}

	if !installer.apply {
		installer.say(
			"rumdl dry-run: would install rumdl==%s via uv tool install (uv=%s)",
			rumdlPinnedVersion,
			provisionedUV,
		)
		return nil
	}

	if installer.options.HarvestOffline {
		installer.skip(fmt.Sprintf("rumdl: offline, will not attempt uv tool install rumdl==%s", rumdlPinnedVersion))
		return nil
	}

	// Prefer the harvestpy-provisioned uv; fall back to a bare "uv" so
	// exec searches $PATH for a system-installed one when the provisioned
	// binary is absent.
	uvPath := provisionedUV
	if info, statErr := os.Stat(uvPath); statErr != nil || info.IsDir() {
		if path, lookupErr := processRunner.LookPath("uv"); lookupErr == nil {
			uvPath = path
		} else {
			installer.skip("rumdl: no uv available (checked provisioned harvestpy uv and PATH)")
			return nil
		}
	}

	binDir := filepath.Join(installer.options.Home, ".local", "bin")
	toolDir := filepath.Join(installer.options.Home, ".local", "share", "uv", "tools", "rumdl")
	var result deps.RunResult
	var runErr error
	journalErr := installer.options.Journal.Write([]string{filepath.Join(binDir, "rumdl"), toolDir}, func() error {
		result, runErr = processRunner.Run(ctx, []string{
			uvPath, "tool", "install", "rumdl==" + rumdlPinnedVersion,
		}, deps.RunOptions{Env: deps.EnvironmentWith("UV_TOOL_BIN_DIR", binDir)})
		return nil
	})
	if journalErr != nil {
		return fmt.Errorf("journal rumdl install: %w", journalErr)
	}
	output := append(append([]byte(nil), result.Stdout...), result.Stderr...)
	if runErr != nil || result.ExitCode != 0 {
		var lookupErr *exec.Error
		if errors.Is(runErr, os.ErrNotExist) || errors.As(runErr, &lookupErr) {
			installer.skip("rumdl: no uv available (checked provisioned harvestpy uv and PATH)")
			return nil
		}
		installer.skip(fmt.Sprintf(
			"rumdl: uv tool install rumdl==%s failed: %v raw=%q",
			rumdlPinnedVersion, runErr, truncateOutput(output, maxRumdlFailureOutput),
		))
		return nil
	}
	installer.ok(fmt.Sprintf("rumdl installed via uv tool install rumdl==%s", rumdlPinnedVersion))
	return nil
}

// rumdlUserConfigPath is where rumdl looks for its user config:
// $XDG_CONFIG_HOME/rumdl/rumdl.toml when XDG_CONFIG_HOME is absolute, else
// ~/.config/rumdl/rumdl.toml — the ~/.config form on macOS too.
func (installer *engine) rumdlUserConfigPath() string {
	configRoot := installer.env().Get("XDG_CONFIG_HOME")
	if !filepath.IsAbs(configRoot) {
		configRoot = filepath.Join(installer.options.Home, ".config")
	}
	return filepath.Join(configRoot, "rumdl", "rumdl.toml")
}

// ensureRumdlUserConfig writes rumdlUserConfig when the host has no rumdl
// user config, so a rumdl run that finds no project config writes no
// .rumdl_cache into its working directory. A present file is the user's and
// is never rewritten; a failure to look or write is a named skip.
func (installer *engine) ensureRumdlUserConfig() error {
	path := installer.rumdlUserConfigPath()
	if _, err := os.Lstat(path); err == nil {
		installer.skip("rumdl user config present, left untouched: " + path)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		installer.skip(fmt.Sprintf("rumdl user config NOT written: stat %s: %v", path, err))
		return nil
	}
	var writeErr error
	journalErr := installer.options.Journal.Write([]string{path}, func() error {
		if !installer.apply {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			writeErr = fmt.Errorf("create %s: %w", filepath.Dir(path), err)
			return writeErr
		}
		if err := atomicfile.Write(path, []byte(rumdlUserConfig), 0o644); err != nil {
			writeErr = fmt.Errorf("write %s: %w", path, err)
		}
		return writeErr
	})
	if writeErr != nil {
		installer.skip("rumdl user config NOT written: " + writeErr.Error())
		return nil
	}
	if journalErr != nil {
		return fmt.Errorf("journal rumdl user config %s: %w", path, journalErr)
	}
	if !installer.apply {
		installer.say("rumdl dry-run: would write user config %s (cache = false)", path)
		return nil
	}
	return installer.change("write rumdl user config -> "+path, nil)
}

// rumdlVersionWithRunner runs `path --version` through the installer's
// process seam and parses rumdl's exact "rumdl X.Y.Z" output shape.
func rumdlVersionWithRunner(ctx context.Context, path string, runner deps.Runner) (string, error) {
	result, err := runner.Run(ctx, []string{path, "--version"}, deps.RunOptions{})
	if err != nil {
		return "", fmt.Errorf("run %s --version: %w", path, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf(
			"run %s --version: exit %d stderr=%q",
			path,
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	line := deps.FirstLine(string(result.Stdout))
	version, ok := strings.CutPrefix(line, "rumdl ")
	if !ok {
		return "", fmt.Errorf("expected \"rumdl \" prefix in %q", line)
	}
	return strings.TrimSpace(version), nil
}

// truncateOutput bounds command output before it reaches an install log line.
func truncateOutput(output []byte, maxBytes int) string {
	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) <= maxBytes {
		return trimmed
	}
	return trimmed[:maxBytes] + "...(truncated)"
}
