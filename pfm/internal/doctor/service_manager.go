package doctor

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
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

// ServiceManagerReport is what probeServiceManager returns: the manager this
// host uses, whether it could be asked at all, and the pfm MCP daemon unit's
// state under it (assets/systemd/pfm-mcp.service, M47;
// assets/launchd/com.professor.pfm.mcp.plist, M48).
type ServiceManagerReport struct {
	Manager string
	Present bool
	Unit    serviceManagerUnitState
}

// serviceManagerProbe is one service manager's doctor rows: the manager word
// the rows print, its binary, the MCP daemon and schedule units it supervises,
// how one unit is asked about, and when the schedule unit counts as armed.
// service_manager_systemd.go and service_manager_launchd.go define one each;
// serviceManagerFor picks between them, so both run in a Linux test.
type serviceManagerProbe struct {
	manager      string
	binary       string
	mcpUnit      string
	scheduleUnit string
	probeUnit    func(context.Context, deps.Runner, string) serviceManagerUnitState
	armed        func(serviceManagerUnitState) bool
}

// serviceManagerFor is kind's doctor rows.
func serviceManagerFor(kind servicemanager.Kind) serviceManagerProbe {
	if kind == servicemanager.KindLaunchd {
		return launchdServiceManager
	}
	return systemdServiceManager
}

// hostServiceManager is this host's rows, chosen once.
var hostServiceManager = serviceManagerFor(servicemanager.Native())

// report asks the manager about unit. A manager binary absent from PATH — the
// container/unmanaged-host case M.06 exercises — is report.Present=false,
// never folded into "could not ask": LookPath failing is a clean, expected
// answer, not a probe that could not run.
func (probe serviceManagerProbe) report(ctx context.Context, runner deps.Runner, unit string) ServiceManagerReport {
	names, err := servicemanager.CurrentUnitNames()
	if err != nil {
		return ServiceManagerReport{
			Manager: probe.manager,
			Present: true,
			Unit:    serviceManagerUnitState{Unit: unit, Err: err},
		}
	}
	unit = names.Name(unit)
	report := ServiceManagerReport{Manager: probe.manager, Unit: serviceManagerUnitState{Unit: unit}}
	if _, err := runner.LookPath(probe.binary); err != nil {
		return report
	}
	report.Present = true
	report.Unit = probe.probeUnit(ctx, runner, unit)
	return report
}

// service asks the manager about the pfm MCP daemon unit.
func (probe serviceManagerProbe) service(ctx context.Context, runner deps.Runner) ServiceManagerReport {
	return probe.report(ctx, runner, probe.mcpUnit)
}

// schedule asks the manager about the pfm schedule unit.
func (probe serviceManagerProbe) schedule(ctx context.Context, runner deps.Runner) ServiceManagerReport {
	return probe.report(ctx, runner, probe.scheduleUnit)
}

// probeServiceManager asks this host's manager about the pfm MCP daemon unit.
func probeServiceManager(ctx context.Context, runner deps.Runner) ServiceManagerReport {
	return hostServiceManager.service(ctx, runner)
}

// probeScheduleTimer asks this host's manager about the pfm schedule unit.
func probeScheduleTimer(ctx context.Context, runner deps.Runner) ServiceManagerReport {
	return hostServiceManager.schedule(ctx, runner)
}

// serviceManagerIdentity names the manager and MCP unit the row reports
// without asking the manager anything.
func serviceManagerIdentity() (manager, unit string, err error) {
	return serviceManagerUnitIdentity(hostServiceManager.mcpUnit)
}

// scheduleTimerIdentity names the manager and schedule unit the schedules row
// reports without asking the manager anything.
func scheduleTimerIdentity() (manager, unit string, err error) {
	return serviceManagerUnitIdentity(hostServiceManager.scheduleUnit)
}

func serviceManagerUnitIdentity(unit string) (string, string, error) {
	names, err := servicemanager.CurrentUnitNames()
	return hostServiceManager.manager, names.Name(unit), err
}

// scheduleTimerArmed reports whether this host's schedule unit fires.
func scheduleTimerArmed(unit serviceManagerUnitState) bool { return hostServiceManager.armed(unit) }

// ServiceManagerProbeOverride lets tests exercise printServiceManagerDoctor's
// three-state formatting without depending on the build's own platform probe
// (systemd on Linux, launchd on Darwin) or a real service manager on the
// host running the test. Production leaves it nil.
var ServiceManagerProbeOverride func(context.Context, deps.Runner) ServiceManagerReport

func configuredServiceManagerProbe(ctx context.Context, runner deps.Runner) ServiceManagerReport {
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
		manager, unit, err := serviceManagerIdentity()
		if err != nil {
			fmt.Fprintf(stdout, "doctor: service-manager=%s unit=%s could_not_ask error=%v\n", manager, unit, err)
			return 1
		}
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
		return "launchctl kickstart -k " + servicemanager.ShellTarget(unit)
	}
	return "systemctl --user enable --now " + unit
}
