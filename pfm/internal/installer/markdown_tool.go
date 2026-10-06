package installer

import (
	"bytes"
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
// is a soft dependency: no path through this step may fail `pfm install`
// (mirrors installHarvest's dry-run gate and ok/skip/say reporting idiom, but
// every terminal state here returns nil).
func (installer *engine) installMarkdownTool(ctx context.Context) error {
	installer.provisionRumdl(ctx)
	installer.ensureRumdlUserConfig()
	return nil
}

// provisionRumdl installs the pinned rumdl unless one at or above the pin is
// present; every outcome is reported as ok, skip or say.
func (installer *engine) provisionRumdl(ctx context.Context) {
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
			return
		}
	}

	if !installer.apply {
		installer.say(
			"rumdl dry-run: would install rumdl==%s via uv tool install (uv=%s)",
			rumdlPinnedVersion,
			provisionedUV,
		)
		return
	}

	if installer.options.HarvestOffline {
		installer.skip(fmt.Sprintf("rumdl: offline, will not attempt uv tool install rumdl==%s", rumdlPinnedVersion))
		return
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
			return
		}
	}

	binDir := filepath.Join(installer.options.Home, ".local", "bin")
	result, runErr := processRunner.Run(ctx, []string{
		uvPath, "tool", "install", "rumdl==" + rumdlPinnedVersion,
	}, deps.RunOptions{Env: deps.EnvironmentWith("UV_TOOL_BIN_DIR", binDir)})
	output := append(append([]byte(nil), result.Stdout...), result.Stderr...)
	if runErr != nil || result.ExitCode != 0 {
		var lookupErr *exec.Error
		if errors.Is(runErr, os.ErrNotExist) || errors.As(runErr, &lookupErr) {
			installer.skip("rumdl: no uv available (checked provisioned harvestpy uv and PATH)")
			return
		}
		installer.skip(fmt.Sprintf(
			"rumdl: uv tool install rumdl==%s failed: %v raw=%q",
			rumdlPinnedVersion, runErr, truncateOutput(output, maxRumdlFailureOutput),
		))
		return
	}
	installer.ok(fmt.Sprintf("rumdl installed via uv tool install rumdl==%s", rumdlPinnedVersion))
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
func (installer *engine) ensureRumdlUserConfig() {
	path := installer.rumdlUserConfigPath()
	if _, err := os.Lstat(path); err == nil {
		installer.skip("rumdl user config present, left untouched: " + path)
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		installer.skip(fmt.Sprintf("rumdl user config NOT written: stat %s: %v", path, err))
		return
	}
	if !installer.apply {
		_ = installer.change("write rumdl user config -> "+path, nil)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		installer.skip(fmt.Sprintf("rumdl user config NOT written: create %s: %v", filepath.Dir(path), err))
		return
	}
	if err := atomicfile.Write(path, []byte(rumdlUserConfig), 0o644); err != nil {
		installer.skip(fmt.Sprintf("rumdl user config NOT written: write %s: %v", path, err))
		return
	}
	_ = installer.change("write rumdl user config -> "+path, nil)
}

func (installer *engine) removeRumdlUserConfig() error {
	path := installer.rumdlUserConfigPath()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read rumdl user config %s: %w", path, err)
	}
	if bytes.Equal(raw, []byte(rumdlUserConfig)) {
		return installer.change("remove rumdl user config "+path, func() error {
			// Gone between the read and the remove is the outcome asked for.
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove rumdl user config %s: %w", path, err)
			}
			return nil
		})
	}
	installer.skip("rumdl user config " + path + " is not pfm's; kept")
	return nil
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
