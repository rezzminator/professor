package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// systemdServiceManager is the doctor's systemd --user rows: the pfm MCP
// daemon unit staged by the installer (internal/installer/assets/systemd/
// pfm-mcp.service, installer.go's mcpUnitName — the daemon M47 names) and the
// timer (assets/systemd/pfm-schedule.timer) that runs pfm internal
// schedule-fire every five minutes. The timer is armed when installed,
// enabled and running: only then does anything fire a due schedule.
var systemdServiceManager = serviceManagerProbe{
	manager:      "systemd",
	binary:       servicemanager.SystemctlBinary,
	mcpUnit:      "pfm-mcp.service",
	scheduleUnit: "pfm-schedule.timer",
	probeUnit:    probeSystemdUnit,
	armed: func(unit serviceManagerUnitState) bool {
		return unit.Present && unit.Enabled && unit.Active
	},
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
	answer := servicemanager.UserSystemctl(probeCtx, runner,
		"show", "--property=LoadState,UnitFileState,ActiveState", unit)
	argv, result := answer.Argv, answer.Result
	if answer.RunErr != nil {
		state.Err = fmt.Errorf("%s: %w", answer.Command(), answer.RunErr)
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
