//go:build darwin

package doctor

import (
	"context"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

const (
	// No supported release is yet proven to contain the macOS lifetime fix.
	herdrLifetimeUnverified = true
	// serviceManagerDarwinName is the value printServiceManagerDoctor
	// reports for the "manager=" field on Darwin.
	serviceManagerDarwinName = "launchd"
	// pfmMCPLaunchdLabel is the label staged by the installer
	// (internal/installer/assets/launchd/com.professor.pfm.mcp.plist,
	// installer/launchd.go's mcpLaunchdLabel) — the daemon M48 names.
	pfmMCPLaunchdLabel = "com.professor.pfm.mcp"
	// pfmScheduleLaunchdLabel is the StartInterval job staged by the installer
	// that runs pfm internal schedule-fire every five minutes.
	pfmScheduleLaunchdLabel = "com.professor.pfm.schedule"
)

// serviceManagerIdentity names the manager and label the row reports without
// asking the manager anything.
func serviceManagerIdentity() (manager, unit string) {
	return serviceManagerDarwinName, pfmMCPLaunchdLabel
}

// probeServiceManager asks launchd about the pfm MCP daemon label. A
// launchctl binary absent from PATH is reported as report.Present=false,
// never folded into "could not ask" — the same absence-vs-error split
// service_manager_linux.go's systemd probe makes.
func probeServiceManager(ctx context.Context, runner deps.Runner) ServiceManagerReport {
	report := ServiceManagerReport{
		Manager: serviceManagerDarwinName,
		Unit:    serviceManagerUnitState{Unit: pfmMCPLaunchdLabel},
	}
	if _, err := runner.LookPath("launchctl"); err != nil {
		return report
	}
	report.Present = true
	report.Unit = probeLaunchdLabel(ctx, runner, pfmMCPLaunchdLabel)
	return report
}

// scheduleTimerIdentity names the manager and label the schedules row
// reports without asking the manager anything.
func scheduleTimerIdentity() (manager, unit string) {
	return serviceManagerDarwinName, pfmScheduleLaunchdLabel
}

// probeScheduleTimer asks launchd about the pfm schedule label; a launchctl
// binary absent from PATH is Present=false, the same clean answer
// probeServiceManager gives.
func probeScheduleTimer(ctx context.Context, runner deps.Runner) ServiceManagerReport {
	report := ServiceManagerReport{
		Manager: serviceManagerDarwinName,
		Unit:    serviceManagerUnitState{Unit: pfmScheduleLaunchdLabel},
	}
	if _, err := runner.LookPath("launchctl"); err != nil {
		return report
	}
	report.Present = true
	report.Unit = probeLaunchdLabel(ctx, runner, pfmScheduleLaunchdLabel)
	return report
}

// scheduleTimerArmed is true when the job is loaded. A StartInterval job
// sits loaded and waiting between fires and reads `running` only mid-fire, so
// Active would report a healthy schedule as dead.
func scheduleTimerArmed(unit serviceManagerUnitState) bool {
	return unit.Present
}

// probeLaunchdLabel reads `launchctl print gui/<uid>/<label>`: a zero exit is
// a loaded label (present, enabled) whose `state = …` line is its state word;
// exit 113 or stderr naming "Could not find service" is launchd not knowing
// the label (nothing staged/loaded — present=false, not an error). A Run
// error, an expired probe or any other non-zero exit (the gui/<uid> domain
// missing, exit 112) is "could not ask", carrying the exit code and stderr.
// "state = not running" contains the substring "running", so the state line
// is compared whole, never with strings.Contains — the same fix
// installer.launchAgentRunning's own comment documents.
func probeLaunchdLabel(ctx context.Context, runner deps.Runner, label string) serviceManagerUnitState {
	state := serviceManagerUnitState{Unit: label}
	probeCtx, cancel := context.WithTimeout(ctx, deps.ProbeTimeout)
	defer cancel()
	answer := servicemanager.NewLaunchd(runner, nil).Print(probeCtx, label)
	if answer.RunErr != nil {
		state.Err = fmt.Errorf("%s: %w", answer.Command(), answer.RunErr)
		return state
	}
	if probeCtx.Err() != nil {
		state.Err = probeUnanswered(probeCtx, answer.Argv, answer.Result)
		return state
	}
	if !answer.OK() {
		if !answer.Unknown() {
			state.Err = probeUnanswered(probeCtx, answer.Argv, answer.Result)
		}
		return state
	}
	state.Present = true
	state.Enabled = true
	state.State = answer.State()
	state.Active = state.State == "running"
	return state
}
