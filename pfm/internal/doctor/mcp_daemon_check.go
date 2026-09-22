package doctor

import (
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
)

// DaemonReachabilityOverride replaces mcpserv.DaemonReachability under test,
// the way HarvestOverride replaces the harvest doctor: nil in production, set
// once by a jailed TestMain so no unit test probes a live daemon on the
// host's loopback port. A test that means to exercise the real probe against
// its own listener sets this back to nil and restores it in t.Cleanup.
var DaemonReachabilityOverride func(config.Runtime) (mcpserv.DaemonStatus, error)

// configuredDaemonReachability returns the override when one is set and
// mcpserv.DaemonReachability otherwise — the same shape as
// configuredHarvestDoctor for HarvestOverride.
func configuredDaemonReachability(runtime config.Runtime) (mcpserv.DaemonStatus, error) {
	if DaemonReachabilityOverride != nil {
		return DaemonReachabilityOverride(runtime)
	}
	return mcpserv.DaemonReachability(runtime)
}

// printMCPDaemonDoctor always reports the local MCP HTTP daemon's
// reachability: a clean "running" row when it answers as itself (with its own
// version-skew warning when the build differs from this binary's), an
// "unreachable" row when nothing answered at all (mcpserv.ErrDaemonAbsent —
// the address is genuinely free), and a distinct "foreign-service" row when
// SOMETHING answered on the configured port but not as pfm's daemon (wrong
// status body, wrong pid, a non-200). The last two are different faults — ours
// being down vs. someone else holding the port — and must never render as the
// same "unreachable" line. A disabled configuration makes an absent daemon a
// reported fact rather than a warning; a running or foreign service is still
// reported normally.
func printMCPDaemonDoctor(stdout io.Writer, runtime config.Runtime) (warnings int) {
	status, daemonErr := configuredDaemonReachability(runtime)
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
		if !mcpConfigured(runtime) {
			fmt.Fprintf(stdout, "doctor: mcp daemon=unreachable disabled-in-config error=%v\n", daemonErr)
			break
		}
		warnings++
		fmt.Fprintf(stdout, "doctor: mcp daemon=unreachable error=%v\n", daemonErr)
	default:
		warnings++
		fmt.Fprintf(stdout, "doctor: mcp daemon=foreign-service error=%v\n", daemonErr)
	}
	return warnings
}
