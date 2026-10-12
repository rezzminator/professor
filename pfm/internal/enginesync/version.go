package enginesync

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/semver"
)

// MinimumVersion is the first engine-sync that compiles machine agent-file
// declarations and excludes abstracts instead of linking them raw.
const MinimumVersion = "0.3.0-alpha.3"

var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)

// CompareVersions orders the wrapper's plain version grammar by SemVer precedence.
func CompareVersions(left, right string) (int, error) {
	if !releaseVersion.MatchString(left) || !releaseVersion.MatchString(right) {
		return 0, fmt.Errorf("invalid engine-sync versions %q and %q", left, right)
	}
	for _, value := range []string{left, right} {
		core, _, _ := strings.Cut(value, "-")
		core, _, _ = strings.Cut(core, "+")
		for _, part := range strings.Split(core, ".") {
			if len(part) > 1 && part[0] == '0' {
				return 0, fmt.Errorf("invalid engine-sync version %q: core part with a leading zero", value)
			}
		}
		if leadingZeroPrereleaseID(value) {
			return 0, fmt.Errorf("invalid engine-sync version %q: numeric prerelease id with a leading zero", value)
		}
	}
	core := func(value string) string {
		return strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '+' })[0]
	}
	a, aOK := semver.ParseVersion("v" + core(left))
	b, bOK := semver.ParseVersion("v" + core(right))
	if !aOK || !bOK {
		return 0, fmt.Errorf("invalid engine-sync version core %q or %q", left, right)
	}
	if a.Less(b) {
		return -1, nil
	}
	if b.Less(a) {
		return 1, nil
	}
	pre := func(value string) string {
		value, _, _ = strings.Cut(value, "+")
		_, suffix, _ := strings.Cut(value, "-")
		return suffix
	}
	aPre, bPre := pre(left), pre(right)
	if aPre == bPre {
		return 0, nil
	}
	if aPre == "" {
		return 1, nil
	}
	if bPre == "" {
		return -1, nil
	}
	x, y := strings.Split(aPre, "."), strings.Split(bPre, ".")
	for index := 0; index < len(x) && index < len(y); index++ {
		if x[index] == y[index] {
			continue
		}
		ax, aErr := strconv.ParseUint(x[index], 10, 64)
		by, bErr := strconv.ParseUint(y[index], 10, 64)
		switch {
		case aErr == nil && bErr == nil:
			if ax < by {
				return -1, nil
			}
			return 1, nil
		case aErr == nil:
			return -1, nil
		case bErr == nil:
			return 1, nil
		case x[index] < y[index]:
			return -1, nil
		default:
			return 1, nil
		}
	}
	if len(x) < len(y) {
		return -1, nil
	}
	return 1, nil
}

// leadingZeroPrereleaseID reports a numeric prerelease id written with a
// leading zero, which semver forbids: "01" and "1" would otherwise each order
// above the other.
func leadingZeroPrereleaseID(value string) bool {
	value, _, _ = strings.Cut(value, "+")
	_, suffix, found := strings.Cut(value, "-")
	if !found {
		return false
	}
	for _, id := range strings.Split(suffix, ".") {
		if len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
			return true
		}
	}
	return false
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
	if result.ExitCode != 0 || !found || !releaseVersion.MatchString(version) {
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
