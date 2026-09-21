//go:build linux

package doctor

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	// serviceManagerLinuxName is the value printServiceManagerDoctor reports
	// for the "manager=" field on Linux.
	serviceManagerLinuxName = "systemd"
	// pfmMCPSystemdUnit is the unit staged by the installer
	// (internal/installer/assets/systemd/pfm-mcp.service, installer.go's
	// mcpUnitName) — the daemon M47 names.
	pfmMCPSystemdUnit = "pfm-mcp.service"
)

// probeServiceManager asks systemd --user about the pfm MCP daemon unit.
// A systemctl binary absent from PATH — the container/unmanaged-host case
// M.06 exercises — is reported as report.Present=false, never folded into
// "could not ask": LookPath failing is a clean, expected answer, not a
// probe that could not run.
func probeServiceManager(ctx context.Context, runner deps.Runner) serviceManagerReport {
	report := serviceManagerReport{
		Manager: serviceManagerLinuxName,
		Unit:    serviceManagerUnitState{Unit: pfmMCPSystemdUnit},
	}
	if _, err := runner.LookPath("systemctl"); err != nil {
		return report
	}
	report.Present = true
	report.Unit = probeSystemdUnit(ctx, runner, pfmMCPSystemdUnit)
	return report
}

// probeSystemdUnit mirrors installer.nameSyncServiceRunning's error reading:
// a completed `systemctl --user is-enabled|is-active` (any exit code) is an
// answer, present=true, decoded from ExitCode; a Run error (lookup lost mid
// probe, context deadline, a non-ExitError failure) is "could not ask" and
// is never read as "not enabled"/"not active".
func probeSystemdUnit(ctx context.Context, runner deps.Runner, unit string) serviceManagerUnitState {
	state := serviceManagerUnitState{Unit: unit, Present: true}
	enabled, err := systemctlUserQuiet(ctx, runner, "is-enabled", unit)
	if err != nil {
		state.Err = err
		return state
	}
	state.Enabled = enabled
	active, err := systemctlUserQuiet(ctx, runner, "is-active", unit)
	if err != nil {
		state.Err = err
		return state
	}
	state.Active = active
	return state
}

// systemctlUserQuiet runs `systemctl --user <verb> --quiet <unit>` bounded
// by deps.ProbeTimeout (the same bound every other doctor version probe
// uses) and reports its exit code as a boolean answer. Only a Run error —
// not a nonzero exit — is treated as "the probe could not run".
func systemctlUserQuiet(ctx context.Context, runner deps.Runner, verb, unit string) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, deps.ProbeTimeout)
	defer cancel()
	result, err := runner.Run(probeCtx, []string{"systemctl", "--user", verb, "--quiet", unit}, deps.RunOptions{})
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}
