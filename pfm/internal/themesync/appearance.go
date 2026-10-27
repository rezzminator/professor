package themesync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// DetectAppearance reads macOS's appearance; other platforms and failed reads
// return an error, never a guessed side. Env.GOOS selects the served platform.
// `defaults read -g AppleInterfaceStyle` prints Dark in dark mode and exits 1
// with "does not exist" when the key is absent (light mode).
func DetectAppearance(ctx context.Context, env Env) (Appearance, error) {
	if !env.darwin() {
		return "", errors.New("themesync: auto appearance detection unsupported; choose an explicit theme/appearance")
	}
	result, err := env.Runner.Run(ctx, []string{"defaults", "read", "-g", "AppleInterfaceStyle"}, deps.RunOptions{})
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
