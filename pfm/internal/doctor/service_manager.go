package doctor

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// serviceManagerUnitState is one supervised unit's probed state under
// whichever platform service manager owns it (systemd on Linux, launchd on
// Darwin). Err is set only when the manager itself could be asked but the
// probe call failed to answer — a genuine "could not ask", never folded
// into "not active" (an error must never render as absence).
type serviceManagerUnitState struct {
	Unit    string
	Present bool
	Enabled bool
	Active  bool
	Err     error
}

// serviceManagerReport is what probeServiceManager — defined once per
// platform in service_manager_linux.go / service_manager_darwin.go, same
// identifier on both so the caller here stays platform-blind (pfm/CLAUDE.md
// § Code Standards, "one binary, two kernels") — returns: the manager this
// platform uses, whether it could be asked at all, and the pfm MCP daemon
// unit's state under it (assets/systemd/pfm-mcp.service, M47;
// assets/launchd/com.professor.pfm.mcp.plist, M48).
type serviceManagerReport struct {
	Manager string
	Present bool
	Unit    serviceManagerUnitState
}

// ServiceManagerProbeOverride lets tests exercise printServiceManagerDoctor's
// three-state formatting without depending on the build's own platform probe
// (systemd on Linux, launchd on Darwin) or a real service manager on the
// host running the test. Production leaves it nil.
var ServiceManagerProbeOverride func(context.Context, deps.Runner) serviceManagerReport

func configuredServiceManagerProbe(ctx context.Context, runner deps.Runner) serviceManagerReport {
	if ServiceManagerProbeOverride != nil {
		return ServiceManagerProbeOverride(ctx, runner)
	}
	return probeServiceManager(ctx, runner)
}

// printServiceManagerDoctor reports the pfm MCP daemon's user-service
// supervision — the row `pfm doctor` had zero of before this (root-cause
// hand-off §3, confirmed absence: no systemd/launchd/unit/is-enabled/
// is-active row anywhere in this package). Three distinct states reach
// stdout, never collapsed into one another:
//
//   - the manager present, with the unit's enabled/active facts;
//   - "no service manager on this host" when the manager binary itself is
//     absent or platform-gated (StateUnavailable) — an expected container
//     or unmanaged-host state, never a failure;
//   - "could not ask" carrying the probe's own error when the manager
//     answered nothing at all.
func printServiceManagerDoctor(ctx context.Context, stdout io.Writer, runner deps.Runner) int {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	report := configuredServiceManagerProbe(ctx, runner)
	if !report.Present {
		fmt.Fprintf(
			stdout,
			"doctor: service-manager=none manager=%s unit=%s state=%s — no %s user manager on this host "+
				"(container or unmanaged); the pfm daemon must be started by hand\n",
			report.Manager, report.Unit.Unit, StateUnavailable, report.Manager,
		)
		return 0
	}
	unit := report.Unit
	if unit.Err != nil {
		fmt.Fprintf(
			stdout,
			"doctor: service-manager=%s unit=%s could_not_ask error=%v\n",
			report.Manager, unit.Unit, unit.Err,
		)
		return 1
	}
	if unit.Present && unit.Enabled && unit.Active {
		fmt.Fprintf(
			stdout,
			"doctor: service-manager=%s unit=%s present=true enabled=true active=true\n",
			report.Manager, unit.Unit,
		)
		return 0
	}
	fmt.Fprintf(
		stdout,
		"doctor: service-manager=%s unit=%s present=%t enabled=%t active=%t — start with: %s\n",
		report.Manager, unit.Unit, unit.Present, unit.Enabled, unit.Active,
		serviceManagerStartHint(report.Manager, unit.Unit),
	)
	return 1
}

// serviceManagerStartHint names the one command that clears the row on each
// platform.
func serviceManagerStartHint(manager, unit string) string {
	if manager == "launchd" {
		return "launchctl kickstart -k gui/$(id -u)/" + unit
	}
	return "systemctl --user enable --now " + unit
}
