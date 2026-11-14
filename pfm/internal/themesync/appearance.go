package themesync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// DetectAppearance reads the system appearance through the served platform's
// probe; a platform without one and a failed read return an error, never a
// guessed side.
func DetectAppearance(ctx context.Context, env Env) (Appearance, error) {
	return env.platform().detectAppearance(ctx, env)
}

// unsupportedAppearance is the probe of a platform with no system appearance to read.
func unsupportedAppearance(context.Context, Env) (Appearance, error) {
	return "", errors.New("themesync: auto appearance detection unsupported; choose an explicit theme/appearance")
}

// detectMacAppearance reads macOS's appearance: `defaults read -g
// AppleInterfaceStyle` prints Dark in dark mode and exits 1 with "does not
// exist" when the key is absent (light mode).
func detectMacAppearance(ctx context.Context, env Env) (Appearance, error) {
	// Resolved through the Runner seam, as herdr is here: a test serving
	// darwin on another host scripts the lookup where deps.Resolve would
	// refuse the darwin-only row for the host's own GOOS.
	defaults, err := env.Runner.LookPath("defaults")
	if err != nil {
		return "", fmt.Errorf("themesync: resolve defaults: %w", err)
	}
	result, err := env.Runner.Run(ctx, []string{defaults, "read", "-g", "AppleInterfaceStyle"}, deps.RunOptions{})
	if err != nil {
		return "", fmt.Errorf("themesync: read macOS appearance: %w", err)
	}
	stdout := strings.TrimSpace(string(result.Stdout))
	stderr := strings.TrimSpace(string(result.Stderr))
	switch {
	case result.ExitCode == 0 && stdout == "Dark":
		return Dark, nil
	case result.ExitCode == 0:
		return "", fmt.Errorf("themesync: macOS appearance reads %q, expected Dark", stdout)
	case result.ExitCode == 1 && strings.Contains(stderr, "does not exist"):
		return Light, nil
	default:
		return "", fmt.Errorf("themesync: defaults read AppleInterfaceStyle exited %d: %s", result.ExitCode, stderr)
	}
}
