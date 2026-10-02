package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	fleetUnitSettle = 3 * time.Second
	systemctlUser   = "--user"
	unitStateActive = "active"
)

func unitStateRunning(state string) (running, known bool) {
	switch state {
	case unitStateActive, "activating", "deactivating", "reloading", "refreshing":
		return true, true
	case "inactive", "failed":
		return false, true
	default:
		return false, false
	}
}

func fleetUnitState(ctx context.Context, runner CommandRunner, unit string) (string, error) {
	args := []string{systemctlUser, "show", "--property=ActiveState", "--value", unit}
	probe := "systemctl " + strings.Join(args, " ")
	reader, ok := runner.(OutputRunner)
	if !ok {
		return "", fmt.Errorf("%s: command runner cannot read output", probe)
	}
	output, err := reader.Output(ctx, "systemctl", args...)
	if err != nil {
		return "", fmt.Errorf("%s: %w", probe, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func verifyFleetUnitsActive(ctx context.Context, runner CommandRunner, units []string) error {
	var failures []error
	for _, unit := range units {
		state, err := fleetUnitState(ctx, runner, unit)
		if err != nil {
			failures = append(failures, fmt.Errorf("fleet unit %s state unreadable after start: %w", unit, err))
			continue
		}
		if state != unitStateActive {
			failures = append(failures, fmt.Errorf(
				"fleet unit %s is %s %s after start — journalctl --user -u %s -n 20",
				unit, state, fleetUnitSettle, unit,
			))
		}
	}
	return errors.Join(failures...)
}

func userSystemctl(ctx context.Context, runner CommandRunner, args ...string) error {
	return runner.Run(ctx, "systemctl", append([]string{systemctlUser}, args...)...)
}
