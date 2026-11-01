package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

const (
	fleetUnitSettle = 3 * time.Second
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
	args := []string{"show", "--property=ActiveState", "--value", unit}
	reader, ok := runner.(OutputRunner)
	if !ok {
		probe := strings.Join(servicemanager.UserSystemctlArgv(args...), " ")
		return "", fmt.Errorf("%s: command runner cannot read output", probe)
	}
	answer := servicemanager.UserSystemctl(ctx, commandOutput{reader}, args...)
	if err := answer.Err(); err != nil {
		return "", fmt.Errorf("%s: %w", answer.Command(), err)
	}
	return strings.TrimSpace(string(answer.Result.Stdout)), nil
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

// userSystemctl runs `systemctl --user args…` once through the install's
// runner.
func (installer *engine) userSystemctl(ctx context.Context, args ...string) servicemanager.Answer {
	return servicemanager.UserSystemctl(ctx, commandRun{installer.options.Runner}, args...)
}
