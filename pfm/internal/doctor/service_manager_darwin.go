//go:build darwin

package doctor

import (
	"context"
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
)

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

// probeLaunchdLabel mirrors installer.launchAgentRunning's two readings: a
// nonzero exit from `launchctl print gui/<uid>/<label>` means launchd does
// not know the label at all (nothing staged/loaded — present=false, not an
// error), while a Run error (the probe never got an answer) is
// "could not ask". "state = not running" contains the substring "running",
// so the state line is compared whole, never with strings.Contains — the
// same fix installer.launchAgentRunning's own comment documents.
func probeLaunchdLabel(ctx context.Context, runner deps.Runner, label string) serviceManagerUnitState {
	state := serviceManagerUnitState{Unit: label}
	probeCtx, cancel := context.WithTimeout(ctx, deps.ProbeTimeout)
	defer cancel()
	result, err := runner.Run(
		probeCtx,
		[]string{"launchctl", "print", "gui/" + strconv.Itoa(os.Getuid()) + "/" + label},
		deps.RunOptions{},
	)
	if err != nil {
		state.Err = err
		return state
	}
	if result.ExitCode != 0 {
		return state
	}
	state.Present = true
	state.Enabled = true
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		if strings.TrimSpace(line) == "state = running" {
			state.Active = true
			break
		}
	}
	return state
}
