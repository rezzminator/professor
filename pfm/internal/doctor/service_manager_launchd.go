package doctor

import (
	"context"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// launchdServiceManager is the doctor's launchd rows: the pfm MCP daemon
// label staged by the installer (internal/installer/assets/launchd/
// com.professor.pfm.mcp.plist, installer/launchd.go's mcpLaunchdLabel — the
// daemon M48 names) and the StartInterval job that runs pfm internal
// schedule-fire every five minutes. That job is armed when loaded: it sits
// loaded and waiting between fires and reads `running` only mid-fire, so
// Active would report a healthy schedule as dead.
var launchdServiceManager = serviceManagerProbe{
	manager:      "launchd",
	binary:       servicemanager.LaunchctlBinary,
	mcpUnit:      "com.professor.pfm.mcp",
	scheduleUnit: "com.professor.pfm.schedule",
	probeUnit:    probeLaunchdLabel,
	armed:        func(unit serviceManagerUnitState) bool { return unit.Present },
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
