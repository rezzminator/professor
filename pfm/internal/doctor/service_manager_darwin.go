//go:build darwin

package doctor

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	// serviceManagerDarwinName is the value printServiceManagerDoctor
	// reports for the "manager=" field on Darwin.
	serviceManagerDarwinName = "launchd"
	// pfmMCPLaunchdLabel is the label staged by the installer
	// (internal/installer/assets/launchd/com.professor.pfm.mcp.plist,
	// installer/launchd.go's mcpLaunchdLabel) — the daemon M48 names.
	pfmMCPLaunchdLabel = "com.professor.pfm.mcp"
	// launchctlNotFoundExit is `launchctl print`'s exit for a label launchd
	// does not know ("Could not find service … in domain").
	launchctlNotFoundExit = 113
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
func probeServiceManager(ctx context.Context, runner deps.Runner) serviceManagerReport {
	report := serviceManagerReport{
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
	argv := []string{"launchctl", "print", "gui/" + strconv.Itoa(os.Getuid()) + "/" + label}
	result, err := runner.Run(probeCtx, argv, deps.RunOptions{})
	if err != nil {
		state.Err = fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		return state
	}
	if probeCtx.Err() != nil {
		state.Err = probeUnanswered(probeCtx, argv, result)
		return state
	}
	if result.ExitCode != 0 {
		if result.ExitCode != launchctlNotFoundExit &&
			!strings.Contains(string(result.Stderr), "Could not find service") {
			state.Err = probeUnanswered(probeCtx, argv, result)
		}
		return state
	}
	state.Present = true
	state.Enabled = true
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "state = "); ok {
			state.State = value
			state.Active = value == "running"
			break
		}
	}
	return state
}
