package doctor

import (
	"fmt"
	"io"
	"os"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
)

// printActivityLogDoctor names the home's activity log and its size — the one
// row that tells an operator where `pfm log` reads from and how much this home
// has written — then the level in force per component (printLogLevelDoctor).
// A file that has never been written is ABSENT (bytes=0); a file that could
// not be stat'd is StateUnavailable with the reason, because "we failed to
// look" is not "there is nothing there". Neither is a warning: an unwritten
// log is a fresh home, and an unreadable one is reported, not tallied.
func printActivityLogDoctor(stdout io.Writer, runtime config.Runtime) {
	path := runtime.Paths.LogFile
	policy := runtime.Config.Log
	info, err := os.Stat(path)
	switch {
	case err == nil:
		fmt.Fprintf(
			stdout,
			"doctor: log path=%s bytes=%d level=%s keep=%d max_mb=%d keep_days=%d\n",
			path, info.Size(), logLevelLabel(policy.Level), policy.KeepFiles, policy.MaxMB, policy.KeepDays,
		)
	case os.IsNotExist(err):
		fmt.Fprintf(
			stdout,
			"doctor: log path=%s bytes=0 state=absent level=%s keep=%d max_mb=%d keep_days=%d\n",
			path, logLevelLabel(policy.Level), policy.KeepFiles, policy.MaxMB, policy.KeepDays,
		)
	default:
		fmt.Fprintf(stdout, "doctor: log path=%s state=%s error=%v\n", path, StateUnavailable, err)
	}
	printLogLevelDoctor(stdout, policy, runtime.Version, paths.OSEnv{})
}

// printLogLevelDoctor prints the level in force per component and where each
// came from — build, config or env — exactly as internal/obs resolves it for a
// process of this build under this environment (spec § Control). An override
// obs refused is printed first with its reason: the rows below it are what
// stands, and a doctor that hid the refusal would misreport the level.
func printLogLevelDoctor(stdout io.Writer, policy config.Log, version string, env paths.Env) {
	levels, err := obs.ResolveLevels(
		obs.Policy{Version: version, Level: policy.Level, Components: policy.Components}, env,
	)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: log control refused: %v\n", err)
	}
	for _, comp := range obs.Components {
		inForce := levels.For(comp)
		fmt.Fprintf(
			stdout, "doctor: log comp=%s level=%s source=%s\n", comp, obs.LevelName(inForce.Level), inForce.Source,
		)
	}
}

// logLevelLabel renders an unset log.level as what it means: the build decides.
func logLevelLabel(level string) string {
	if level == "" {
		return "build"
	}
	return level
}
