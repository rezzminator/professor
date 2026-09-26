package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// installMarkdownTool provisions rumdl, the markdown linter/formatter the
// framework's prompts and docs are checked against. It is a soft
// dependency: no path through this step may fail `pfm install` (mirrors
// installHarvest's dry-run gate and ok/skip/say reporting idiom, but every
// terminal state here returns nil).
func (installer *engine) installMarkdownTool(ctx context.Context) error {
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
			uvPath = "uv"
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
