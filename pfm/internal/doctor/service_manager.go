package doctor

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// serviceManagerUnitState is one supervised unit's probed state under
// whichever platform service manager owns it (systemd on Linux, launchd on
// Darwin). Present starts false and is set only by the manager's answer;
// State is the manager's own word for the unit (systemd's ActiveState,
// launchd's `state = …`). Err is set when the manager binary resolved but
// gave no answer — a failed run, a non-answer exit, an expired probe — a
// genuine "could not ask", never folded into "not active" (an error must
// never render as absence).
type serviceManagerUnitState struct {
	Unit    string
	Present bool
	Enabled bool
	Active  bool
	State   string
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
//
// With every MCP server disabled in config there is no daemon to supervise:
// the row says disabled-in-config and the manager is never asked.
func printServiceManagerDoctor(ctx context.Context, stdout io.Writer, runner deps.Runner, runtime config.Runtime) int {
	if !mcpConfigured(runtime) {
		manager, unit := serviceManagerIdentity()
		fmt.Fprintf(stdout, "doctor: service-manager=%s unit=%s disabled-in-config\n", manager, unit)
		return 0
	}
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
			"doctor: service-manager=%s unit=%s present=true enabled=true active=%s\n",
			report.Manager, unit.Unit, unit.State,
		)
		return 0
	}
	state := unit.State
	if state == "" {
		state = "none"
	}
	hint := "run pfm install --yes"
	if unit.Present {
		hint = "start with: " + serviceManagerStartHint(report.Manager, unit.Unit)
	}
	fmt.Fprintf(
		stdout,
		"doctor: service-manager=%s unit=%s present=%t enabled=%t active=%s — %s\n",
		report.Manager, unit.Unit, unit.Present, unit.Enabled, state, hint,
	)
	return 1
}

// probeUnanswered is the could-not-ask error for a manager probe that ran but
// gave no answer: a probe killed at its deadline (RealRunner reports a nil Run
// error for it) names the deadline; any other failure names its exit code and
// stderr.
func probeUnanswered(probeCtx context.Context, argv []string, result deps.RunResult) error {
	if err := probeCtx.Err(); err != nil {
		return fmt.Errorf(
			"%s did not answer within the %s deadline: %w",
			strings.Join(argv, " "),
			deps.ProbeTimeout,
			err,
		)
	}
	return fmt.Errorf(
		"%s exit=%d stderr=%q",
		strings.Join(argv, " "),
		result.ExitCode,
		strings.TrimSpace(string(result.Stderr)),
	)
}

// serviceManagerStartHint names the one command that starts a staged unit on
// each platform.
func serviceManagerStartHint(manager, unit string) string {
	if manager == "launchd" {
		return "launchctl kickstart -k gui/$(id -u)/" + unit
	}
	return "systemctl --user enable --now " + unit
}
