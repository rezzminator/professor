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

// unitStateActive is the systemd ActiveState of a unit that is up.
const unitStateActive = "active"

// layoutServiceUnits are the systemd user units that hold a pfm database.
var layoutServiceUnits = []string{mcpUnitName, nameSyncPathUnit, nameSyncTimerUnit}

// unitStateRunning reads one systemd ActiveState word: a unit doing work
// (active, activating, deactivating, reloading, refreshing) is running;
// inactive or failed is idle (systemd reports an unknown unit as inactive).
// known is false for any other word — the probe got no answer it can trust.
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
		if state != unitStateActive {
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

// probeLayoutServices answers, read-only, which pfm services stopLayoutServices
// would stop now — the name-sync scheduler always, the MCP service only withMCP
// (database work). An unreachable user manager answers none; a state probe
// that cannot answer is the error, exactly where the stop refuses.
func probeLayoutServices(ctx context.Context, env LayoutEnv, withMCP bool) ([]string, error) {
	runner := env.commandRunner()
	var running []string
	var failures []error
	if schedulerIsLaunchd {
		for _, label := range []string{mcpLaunchdLabel, launchdLabel} {
			if label == mcpLaunchdLabel && !withMCP {
				continue
			}
			service := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
			loaded, err := launchctlPrintLoaded(service, runner.Run(ctx, "launchctl", "print", service))
			if err != nil {
				failures = append(failures, err)
			} else if loaded {
				running = append(running, label)
			}
		}
	} else {
		if layoutSystemctl(ctx, runner, "show-environment") != nil {
			return nil, nil
		}
		for _, unit := range layoutServiceUnits {
			if unit == mcpUnitName && !withMCP {
				continue
			}
			state, err := fleetUnitState(ctx, runner, unit)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			isRunning, known := unitStateRunning(state)
			if !known {
				failures = append(failures, fmt.Errorf("fleet unit %s reports unknown ActiveState %q", unit, state))
			} else if isRunning {
				running = append(running, unit)
			}
		}
	}
	if len(failures) != 0 {
		return nil, errors.Join(failures...)
	}
	return running, nil
}

// PreviewLayoutStop is `pfm install`'s preview of the apply's service stop:
// when the layout has work, it runs the stop's read-only probes and returns
// the error the stop would refuse with; nil when the stop would proceed or the
// layout has no work (the apply then stops nothing).
func PreviewLayoutStop(ctx context.Context, env LayoutEnv, findings []LayoutFinding) error {
	if !layoutApplyWork(env, findings) {
		return nil
	}
	_, dbWork := layoutDatabaseWork(findings)
	_, err := probeLayoutServices(ctx, env, dbWork)
	return err
}

// stopLayoutServices stops the pfm services running now (probeLayoutServices)
// and returns them, so the restart brings back exactly those: a unit the host
// never loaded, or one the operator stopped, is neither stopped nor started. An
// unreachable user manager stops nothing; the database holder scan then
// decides. A probe that cannot answer is an error and stops nothing.
func stopLayoutServices(ctx context.Context, env LayoutEnv, withMCP bool) ([]string, error) {
	running, err := probeLayoutServices(ctx, env, withMCP)
	if err != nil || len(running) == 0 {
		return nil, err
	}
	runner := env.commandRunner()
	if schedulerIsLaunchd {
		var failures []error
		var stopped []string
		for _, label := range running {
			service := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
			if err := runner.Run(ctx, "launchctl", "bootout", service); err != nil {
				failures = append(failures, fmt.Errorf("launchctl bootout %s: %w", service, err))
				continue
			}
			stopped = append(stopped, label)
		}
		return stopped, errors.Join(append(failures, awaitLaunchdTeardown(ctx, env, runner, stopped))...)
	}
	if err := layoutSystemctl(ctx, runner, append([]string{"stop"}, running...)...); err != nil {
		return running, fmt.Errorf("systemctl --user stop layout services: %w", err)
	}
	return running, nil
}

// launchctlNotLoaded is `launchctl print`'s exit status for a job its domain
// has not loaded.
const launchctlNotLoaded = 113

// launchdTeardownAttempts bounds awaitLaunchdTeardown's polls, one
// launchdBootstrapRetryInterval apart: 25 s for every label together, past
// launchd's default ExitTimeOut (20 s) for a job slow to exit on SIGTERM.
const launchdTeardownAttempts = 250

// awaitLaunchdTeardown waits until each booted-out label reads not loaded
// (exit 113) — a bootout only requests the teardown — so the name-sync re-probe
// and the holder rescan never see a job launchd is still tearing down. A label
// still loaded once the bound ran out, or a print that cannot answer, is the
// error, and the apply refuses before any write; its caller names the rerun.
func awaitLaunchdTeardown(ctx context.Context, env LayoutEnv, runner CommandRunner, labels []string) error {
	attempts := 0
	for _, label := range labels {
		service := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
		for {
			loaded, err := launchctlPrintLoaded(service, runner.Run(ctx, "launchctl", "print", service))
			if err != nil || !loaded {
				if err != nil {
					return fmt.Errorf("launch agent %s teardown after its bootout: %w", label, err)
				}
				break
			}
			attempts++
			if attempts >= launchdTeardownAttempts {
				return fmt.Errorf("launch agent %s still tearing down %s after its bootout",
					label, launchdTeardownAttempts*launchdBootstrapRetryInterval)
			}
			if err := env.pause(ctx, launchdBootstrapRetryInterval); err != nil {
				return fmt.Errorf("launch agent %s teardown: %w", label, err)
			}
		}
	}
	return nil
}

// launchctlPrintLoaded reads a `launchctl print` outcome: success is a loaded
// job and exit 113 one not loaded; any other failure (a timeout, a missing
// launchctl) cannot answer, and is the error, never "not loaded".
func launchctlPrintLoaded(service string, err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) && exit.ExitCode() == launchctlNotLoaded {
		return false, nil
	}
	return false, fmt.Errorf("launchctl print %s: %w", service, err)
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
		return restartLaunchdLabels(ctx, env, runner, stopped, false)
	}
	if err := layoutSystemctl(ctx, runner, append([]string{"start"}, stopped...)...); err != nil {
		return fmt.Errorf("systemctl --user start layout services: %w", err)
	}
	if err := env.settleServices(ctx); err != nil {
		return err
	}
	return verifyFleetUnitsActive(ctx, runner, stopped)
}

// restartLaunchdLabels bootstraps each stopped launch agent again — retried
// while launchd finishes the teardown its bootout only requested
// (launchctlBootstrapWithRetry) — waits the settle and verifies each loaded.
// launchd answers a bootstrap of a loaded label with the same failure as one
// still tearing down, so a label that reads loaded after its bootstrap failed
// counts as already loaded only when loadedIsBack: its teardown was seen
// complete, and something else (installer.Run) bootstrapped it since. A label
// left failing names its manual bootstrap.
func restartLaunchdLabels(
	ctx context.Context,
	env LayoutEnv,
	runner CommandRunner,
	labels []string,
	loadedIsBack bool,
) error {
	var failures []error
	var started []string
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	for _, label := range labels {
		plist := launchAgentPlist(env, label)
		if err := launchctlBootstrapWithRetry(ctx, runner, env.pause, domain, plist); err != nil {
			if loadedIsBack {
				service := domain + "/" + label
				if loaded, printErr := launchctlPrintLoaded(
					service,
					runner.Run(ctx, "launchctl", "print", service),
				); printErr == nil &&
					loaded {
					started = append(started, label)
					continue
				} else if printErr != nil {
					err = errors.Join(err, printErr)
				}
			}
			failures = append(
				failures,
				fmt.Errorf("launchctl bootstrap %s: %w — start it by hand: launchctl bootstrap %s %s",
					plist, err, domain, shellCommandLine(plist)),
			)
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
		service := domain + "/" + label
		if loaded, err := launchctlPrintLoaded(service, runner.Run(ctx, "launchctl", "print", service)); err != nil {
			failures = append(failures, err)
		} else if !loaded {
			failures = append(failures, fmt.Errorf("launch agent %s not loaded after bootstrap", label))
		}
	}
	return errors.Join(failures...)
}

// pause waits d through the env's settle seam; nil is the real clock.
func (env LayoutEnv) pause(ctx context.Context, d time.Duration) error {
	if env.settle != nil {
		env.settle(d)
		return nil
	}
	if err := clock.Real.Sleep(ctx, d); err != nil {
		return fmt.Errorf("wait %s before the next launchctl bootstrap: %w", d, err)
	}
	return nil
}

// launchAgentPlist is the plist a stopped launchd label is bootstrapped from.
func launchAgentPlist(env LayoutEnv, label string) string {
	return filepath.Join(env.Home, "Library", "LaunchAgents", label+".plist")
}
