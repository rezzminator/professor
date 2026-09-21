package doctor

import (
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
)

// printMCPDaemonDoctor reports the local MCP HTTP daemon's reachability: a
// clean "running" row when it answers as itself (with its own version-skew
// warning when the build differs from this binary's), an "unreachable" row
// when nothing answered at all (mcpserv.ErrDaemonAbsent — the address is
// genuinely free), and a distinct "foreign-service" row when SOMETHING
// answered on the configured port but not as pfm's daemon (wrong status
// body, wrong pid, a non-200). The last two are different faults — ours
// being down vs. someone else holding the port — and must never render as
// the same "unreachable" line.
func printMCPDaemonDoctor(stdout io.Writer, runtime config.Runtime) (warnings int) {
	status, daemonErr := mcpserv.DaemonReachability(runtime)
	switch {
	case daemonErr == nil:
		fmt.Fprintf(
			stdout,
			"doctor: mcp daemon=running pid=%d since=%s endpoint=%s\n",
			status.PID,
			status.StartTime,
			status.Endpoint,
		)
		warnings += printHarvesterExternalDoctor(stdout, runtime.Config.Harvester, status.HarvesterExternal)
		if status.PFMVersion != runtime.Version {
			warnings++
			fmt.Fprintf(
				stdout,
				"doctor: mcp daemon=version-skew daemon=%s client=%s\n",
				status.PFMVersion,
				runtime.Version,
			)
		}
	case errors.Is(daemonErr, mcpserv.ErrDaemonAbsent):
		warnings++
		fmt.Fprintf(stdout, "doctor: mcp daemon=unreachable error=%v\n", daemonErr)
	default:
		warnings++
		fmt.Fprintf(stdout, "doctor: mcp daemon=foreign-service error=%v\n", daemonErr)
	}
	return warnings
}
