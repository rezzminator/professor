package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printLaunchAgentsDoctor holds every pfm launch agent on this host to the
// stable launcher (agentlauncher): an agent whose ProgramArguments[0] is pfm
// itself makes macOS re-register it, and raise a background-activity advisory,
// after each rebuild. The row prints only where an agent exists, so a host
// without launchd prints nothing; a plist it could not parse is a drift row,
// never skipped. It returns its warnings.
func printLaunchAgentsDoctor(stdout io.Writer, home string) int {
	agents, findings := installer.LaunchAgentLauncherDrift(home)
	if agents == 0 {
		return 0
	}
	if len(findings) == 0 {
		fmt.Fprintf(stdout, "doctor: launch-agents agents=%d launcher=in-sync\n", agents)
		return 0
	}
	for _, finding := range findings {
		fmt.Fprintf(
			stdout,
			"doctor: launch-agents drift=%s — %s — %s\n",
			finding.Path, finding.Detail, finding.Fix,
		)
	}
	return len(findings)
}
