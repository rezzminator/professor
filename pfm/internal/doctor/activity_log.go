package doctor

import (
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printActivityLogDoctor names the home's log root (paths.LogRoot) and its
// activity log's current segment and size — the one row that tells an operator
// where `pfm log` reads from — with the retention in force, then the level in
// force per component (printLogLevelDoctor). A segment never written is ABSENT
// (bytes=0); one that could not be stat'd is StateUnavailable with the reason,
// because "we failed to look" is not "there is nothing there". Neither is a
// warning: an unwritten log is a fresh home, and an unreadable one is
// reported, not tallied. A recorded clone the log cannot live in is one
// warning, its reason on its own row. It returns the warnings it counted.
func printActivityLogDoctor(stdout io.Writer, runtime config.Runtime, env paths.Env) int {
	place := paths.LogRoot(env, runtime.Paths.Home)
	path := place.Activity()
	policy := runtime.Config.Log
	retention := fmt.Sprintf("segment_mb=%d keep_mb=%d", policy.MaxMB, policy.KeepFiles*policy.MaxMB)
	info, err := os.Stat(path)
	switch {
	case err == nil:
		fmt.Fprintf(stdout, "doctor: log path=%s bytes=%d level=%s %s\n",
			path, info.Size(), logLevelLabel(policy.Level), retention)
	case os.IsNotExist(err):
		fmt.Fprintf(stdout, "doctor: log path=%s bytes=0 state=absent level=%s %s\n",
			path, logLevelLabel(policy.Level), retention)
	default:
		fmt.Fprintf(stdout, "doctor: log path=%s state=%s error=%v\n", path, StateUnavailable, err)
	}
	warnings := 0
	if place.Fallback != nil {
		fmt.Fprintf(stdout, "doctor: log WARN root=%s: %v\n", place.Dir, place.Fallback)
		warnings = 1
	}
	return warnings + printLogLevelDoctor(stdout, policy, runtime.Version, env)
}

// printLogLevelDoctor prints the level in force per component and where each
// came from — build, config or env — exactly as internal/obs resolves it for a
// process of this build under this environment (spec § Control). An override
// obs refused is printed first with its reason: the rows below it are what
// stands, and a doctor that hid the refusal would misreport the level. The
// refusal is one warning: the operator believes a level is in force that is not.
func printLogLevelDoctor(stdout io.Writer, policy config.Log, version string, env paths.Env) (warnings int) {
	levels, err := obs.ResolveLevels(
		obs.Policy{Version: version, Level: policy.Level, Components: policy.Components}, env,
	)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: log control refused: %v\n", err)
		warnings = 1
	}
	for _, comp := range obs.Components {
		inForce := levels.For(comp)
		fmt.Fprintf(
			stdout, "doctor: log comp=%s level=%s source=%s\n", comp, obs.LevelName(inForce.Level), inForce.Source,
		)
	}
	return warnings
}

// logLevelLabel renders an unset log.level as what it means: the build decides.
func logLevelLabel(level string) string {
	if level == "" {
		return "build"
	}
	return level
}
