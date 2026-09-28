package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// fleetUnitSettle is how long a started fleet unit gets before its state is
// read back: long enough for a unit that dies at startup to leave "active".
const fleetUnitSettle = 3 * time.Second

// systemctlUser puts a systemctl call on the user manager.
const systemctlUser = "--user"

// layoutServiceUnits are the systemd user units that hold a pfm database.
var layoutServiceUnits = []string{mcpUnitName, nameSyncPathUnit, nameSyncTimerUnit}

// unitStateRunning reads one systemd ActiveState word: a unit doing work
// (active, activating, deactivating, reloading, refreshing) is running;
// inactive or failed is idle (systemd reports an unknown unit as inactive).
// known is false for any other word — the probe got no answer it can trust.
func unitStateRunning(state string) (running, known bool) {
	switch state {
	case "active", "activating", "deactivating", "reloading", "refreshing":
		return true, true
	case "inactive", "failed":
		return false, true
	default:
		return false, false
	}
}

// fleetUnitState reads a user unit's ActiveState. A runner that cannot read
// output, or a systemctl that fails, is an error, never an idle unit.
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

// verifyFleetUnitsActive reads back every started unit after the settle; any
// state but active, or a state it cannot read, is an error naming the unit.
func verifyFleetUnitsActive(ctx context.Context, runner CommandRunner, units []string) error {
	var failures []error
	for _, unit := range units {
		state, err := fleetUnitState(ctx, runner, unit)
		if err != nil {
			failures = append(failures, fmt.Errorf("fleet unit %s state unreadable after start: %w", unit, err))
			continue
		}
		if state != "active" {
			failures = append(failures, fmt.Errorf(
				"fleet unit %s is %s %s after start — journalctl --user -u %s -n 20",
				unit, state, fleetUnitSettle, unit,
			))
		}
	}
	return errors.Join(failures...)
}

// settleServices waits the settle through the env's seam; nil is the real
// clock.
func (env LayoutEnv) settleServices(ctx context.Context) error {
	if env.settle != nil {
		env.settle(fleetUnitSettle)
		return nil
	}
	if err := clock.Real.Sleep(ctx, fleetUnitSettle); err != nil {
		return fmt.Errorf("wait %s for fleet units to settle: %w", fleetUnitSettle, err)
	}
	return nil
}

// stopLayoutServices stops the pfm services running now and returns them, so
// the restart brings back exactly those: a unit the host never loaded, or one
// the operator stopped, is neither stopped nor started. An unreachable user
// manager stops nothing; the database holder scan then decides. A state probe
// that cannot answer is an error and stops nothing.
func stopLayoutServices(ctx context.Context, env LayoutEnv) ([]string, error) {
	runner := env.commandRunner()
	var running []string
	if schedulerIsLaunchd {
		var failures []error
		for _, label := range []string{mcpLaunchdLabel, launchdLabel} {
			service := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
			if runner.Run(ctx, "launchctl", "print", service) != nil {
				continue
			}
			if err := runner.Run(ctx, "launchctl", "bootout", service); err != nil {
				failures = append(failures, fmt.Errorf("launchctl bootout %s: %w", service, err))
				continue
			}
			running = append(running, label)
		}
		return running, errors.Join(failures...)
	}
	if layoutSystemctl(ctx, runner, "show-environment") != nil {
		return nil, nil
	}
	var failures []error
	for _, unit := range layoutServiceUnits {
		state, err := fleetUnitState(ctx, runner, unit)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		isRunning, known := unitStateRunning(state)
		if !known {
			failures = append(failures, fmt.Errorf("fleet unit %s reports unknown ActiveState %q", unit, state))
			continue
		}
		if isRunning {
			running = append(running, unit)
		}
	}
	if len(failures) != 0 {
		return nil, errors.Join(failures...)
	}
	if len(running) == 0 {
		return nil, nil
	}
	if err := layoutSystemctl(ctx, runner, append([]string{"stop"}, running...)...); err != nil {
		return running, fmt.Errorf("systemctl --user stop layout services: %w", err)
	}
	return running, nil
}

// layoutSystemctl runs one systemctl verb on the user manager.
func layoutSystemctl(ctx context.Context, runner CommandRunner, args ...string) error {
	return runner.Run(ctx, "systemctl", append([]string{systemctlUser}, args...)...)
}

// restartLayoutServices starts again the services stopLayoutServices stopped,
// waits the settle and verifies every one came back.
func restartLayoutServices(ctx context.Context, env LayoutEnv, stopped []string) error {
	if len(stopped) == 0 {
		return nil
	}
	runner := env.commandRunner()
	if schedulerIsLaunchd {
		var failures []error
		var started []string
		for _, label := range stopped {
			service := launchAgentPlist(env, label)
			if err := runner.Run(
				ctx,
				"launchctl",
				"bootstrap",
				fmt.Sprintf("gui/%d", os.Getuid()),
				service,
			); err != nil {
				failures = append(failures, fmt.Errorf("launchctl bootstrap %s: %w", service, err))
				continue
			}
			started = append(started, label)
		}
		if len(started) == 0 {
			return errors.Join(failures...)
		}
		if err := env.settleServices(ctx); err != nil {
			return errors.Join(append(failures, err)...)
		}
		for _, label := range started {
			if runner.Run(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)) != nil {
				failures = append(failures, fmt.Errorf("launch agent %s not loaded after bootstrap", label))
			}
		}
		return errors.Join(failures...)
	}
	if err := layoutSystemctl(ctx, runner, append([]string{"start"}, stopped...)...); err != nil {
		return fmt.Errorf("systemctl --user start layout services: %w", err)
	}
	if err := env.settleServices(ctx); err != nil {
		return err
	}
	return verifyFleetUnitsActive(ctx, runner, stopped)
}

// launchAgentPlist is the plist a stopped launchd label is bootstrapped from.
func launchAgentPlist(env LayoutEnv, label string) string {
	return filepath.Join(env.Home, "Library", "LaunchAgents", label+".plist")
}
