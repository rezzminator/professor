//go:build linux

package doctor

import (
	"context"
	"fmt"
	"strings"

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

// serviceManagerIdentity names the manager and unit the row reports without
// asking the manager anything.
func serviceManagerIdentity() (manager, unit string) {
	return serviceManagerLinuxName, pfmMCPSystemdUnit
}

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

// probeSystemdUnit asks systemd --user once, `systemctl --user show
// --property=LoadState,UnitFileState,ActiveState <unit>`, bounded by
// deps.ProbeTimeout (the bound every other doctor probe uses). A completed show
// is an answer: LoadState not-found is a unit never staged (present=false),
// UnitFileState enabled/enabled-runtime is enabled, ActiveState is the state
// word and `active` alone is active. A Run error, a non-zero exit (a dead user
// bus), an expired probe or an answer without the three keys is "could not
// ask" — never read as "not enabled"/"not active".
func probeSystemdUnit(ctx context.Context, runner deps.Runner, unit string) serviceManagerUnitState {
	state := serviceManagerUnitState{Unit: unit}
	probeCtx, cancel := context.WithTimeout(ctx, deps.ProbeTimeout)
	defer cancel()
	argv := []string{"systemctl", "--user", "show", "--property=LoadState,UnitFileState,ActiveState", unit}
	result, err := runner.Run(probeCtx, argv, deps.RunOptions{})
	if err != nil {
		state.Err = fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
		return state
	}
	if result.ExitCode != 0 || probeCtx.Err() != nil {
		state.Err = probeUnanswered(probeCtx, argv, result)
		return state
	}
	properties := map[string]string{}
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			properties[key] = value
		}
	}
	for _, key := range []string{"LoadState", "UnitFileState", "ActiveState"} {
		if _, ok := properties[key]; !ok {
			state.Err = fmt.Errorf("%s answered without %s: %q", strings.Join(argv, " "), key, result.Stdout)
			return state
		}
	}
	state.Present = properties["LoadState"] != "not-found"
	state.Enabled = properties["UnitFileState"] == "enabled" || properties["UnitFileState"] == "enabled-runtime"
	state.State = properties["ActiveState"]
	state.Active = state.State == "active"
	return state
}
