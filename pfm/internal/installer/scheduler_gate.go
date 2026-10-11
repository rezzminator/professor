package installer

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/rezzminator/professor/pfm/internal/schedule"
)

const (
	launchGateUnprobedNote   = "launch-agent gate NOT probed (launchctl print could not run or its output could not be read); an apply during a name-sync or schedule run is not refused"
	nameSyncGateUnprobedNote = "name-sync gate NOT probed (systemctl show could not read the unit state); an apply during a name-sync or schedule run is not refused"
	scheduleGatePoll         = 5 * time.Second
)

// schedulerServiceRunning reports whether the Linux scheduler service unit
// (pfm-name-sync.service, pfm-schedule.service) is executing right now, and
// whether the question could be asked at all.
//
// Each is Type=oneshot: while it runs its state is "activating", and
// `systemctl is-active` exits 3 for that exactly as it does for "inactive" — an
// exit-code gate reads a running job as idle and can never refuse. So the state
// is read by name from `systemctl show`: a unit doing work (active, activating,
// deactivating, reloading, refreshing) is running; inactive or failed is idle
// (systemd reports an unknown unit as inactive). Anything else — a non-zero exit
// such as "Failed to connect to bus", systemctl missing from PATH, a runner that
// cannot read output, a state this gate does not know — means the probe never
// got an answer, and the caller must not read that silence as safety.
func schedulerServiceRunning(ctx context.Context, runner CommandRunner, unit string) (running, probed bool) {
	state, err := fleetUnitState(ctx, runner, unit)
	if err != nil {
		return false, false
	}
	return unitStateRunning(state)
}

func CheckScheduler(
	ctx context.Context,
	runner CommandRunner,
	sleepers ...func(time.Duration),
) (unprobed string, err error) {
	if runner == nil {
		runner = execCommandRunner{}
	}
	options := Options{Runner: runner, Stdout: io.Discard}
	if len(sleepers) > 0 {
		options.Sleep = sleepers[0]
	}
	probed, err := awaitSchedulerGate(ctx, options)
	if !probed {
		if schedulerIsLaunchd {
			return launchGateUnprobedNote, err
		}
		return nameSyncGateUnprobedNote, err
	}
	return "", err
}

func schedulerGate(ctx context.Context, runner CommandRunner) (probed bool, err error) {
	if schedulerIsLaunchd {
		syncRunning, syncAnswered := launchAgentRunning(ctx, runner, launchdLabel)
		if syncRunning {
			return true, ErrLaunchAgentRunning
		}
		scheduleRunning, scheduleAnswered := launchAgentRunning(ctx, runner, scheduleLaunchdLabel)
		if scheduleRunning {
			return true, ErrScheduleAgentRunning
		}
		return syncAnswered && scheduleAnswered, nil
	}
	syncRunning, syncAnswered := schedulerServiceRunning(ctx, runner, "pfm-name-sync.service")
	if syncRunning {
		return true, ErrNameSyncRunning
	}
	scheduleRunning, scheduleAnswered := schedulerServiceRunning(ctx, runner, scheduleServiceUnit)
	if scheduleRunning {
		return true, ErrScheduleRunning
	}
	return syncAnswered && scheduleAnswered, nil
}

func SchedulerRefusal(command string, err error) string {
	switch {
	case errors.Is(err, ErrNameSyncRunning):
		return "pfm " + command +
			": the pfm name-sync service is running; wait for it to finish or run `systemctl --user stop pfm-name-sync.service`, then retry"
	case errors.Is(err, ErrScheduleRunning):
		return "pfm " + command +
			": the pfm schedule service is running; wait for it to finish or run `systemctl --user stop pfm-schedule.service`, then retry"
	case errors.Is(err, ErrScheduleAgentRunning):
		return "pfm " + command +
			": the pfm schedule launch agent is running; wait for it to finish or `launchctl bootout gui/$(id -u)/com.professor.pfm.schedule` first"
	case errors.Is(err, ErrLaunchAgentRunning):
		return "pfm " + command +
			": the pfm name-sync launch agent is running; wait for it to finish or `launchctl bootout gui/$(id -u)/com.professor.pfm.name-sync` first"
	}
	return ""
}

func awaitSchedulerGate(ctx context.Context, options Options) (probed bool, err error) {
	probed, err = schedulerGate(ctx, options.Runner)
	if !errors.Is(err, ErrScheduleRunning) && !errors.Is(err, ErrScheduleAgentRunning) {
		return probed, err
	}
	waiter := &engine{options: options}
	waiter.say("  wait    the pfm schedule is delivering; waiting up to %s for it to finish", schedule.ComposerWait)
	for waited := time.Duration(0); waited < schedule.ComposerWait; waited += scheduleGatePoll {
		waiter.pause(scheduleGatePoll)
		probed, err = schedulerGate(ctx, options.Runner)
		if !errors.Is(err, ErrScheduleRunning) && !errors.Is(err, ErrScheduleAgentRunning) {
			return probed, err
		}
	}
	return probed, err
}
