package enginesync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/semver"
)

// MinimumVersion is the first engine-sync that reads its machine config from
// one global file, {home}/.claude/engine-sync.json, and refuses the old
// {config}/engine-sync/config.json and targets.json beside it.
const MinimumVersion = "0.4.0-alpha"

// CompareVersions orders the wrapper's plain version grammar,
// MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD] with no "v", by SemVer precedence.
func CompareVersions(left, right string) (int, error) {
	for _, value := range []string{left, right} {
		if !validVersion(value) {
			return 0, fmt.Errorf("invalid engine-sync version %q", value)
		}
	}
	return semver.Compare("v"+left, "v"+right), nil
}

// validVersion refuses short forms, leading zeros and a "v" prefix.
func validVersion(value string) bool {
	return semver.Valid("v" + value)
}

// Version reads the selected binary's version without loading its configuration.
func (runner Runner) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := runner.run(ctx, []string{"version"})
	if err != nil {
		return "", err
	}
	report := strings.TrimSpace(string(result.Stdout))
	version, found := strings.CutPrefix(report, "engine-sync ")
	if result.ExitCode != 0 || !found || !validVersion(version) {
		return "", fmt.Errorf(
			"engine-sync version: unusable report %q (exit %d; %s)",
			report,
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	return version, nil
}

// RequireMinimumVersion refuses a binary that cannot read Professor's shipped policy.
func (runner Runner) RequireMinimumVersion(ctx context.Context) error {
	version, err := runner.Version(ctx)
	if err != nil {
		return err
	}
	order, err := CompareVersions(version, MinimumVersion)
	if err != nil {
		return err
	}
	if order < 0 {
		return fmt.Errorf(
			"engine-sync %s is older than required %s — update the plugin (`claude plugin update engine-sync@professor`), "+
				"then start a new Claude session so its wrapper links the newer binary",
			version,
			MinimumVersion,
		)
	}
	return nil
}
