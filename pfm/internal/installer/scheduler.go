package installer

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// scheduler is the service manager pfm's scheduled jobs (name-sync, the
// schedule tick, the MCP daemon) run under: launchd agents on macOS, systemd
// user units on Linux. One binary carries both; schedulerFor picks one from a
// servicemanager.Kind, Options.jobScheduler serves the host's by default, and a
// test hands either kind to Run, so both install paths run on one host.
type scheduler interface {
	// gate asks whether a scheduled pfm job is executing right now, and
	// whether the question could be asked at all.
	gate(ctx context.Context, runner CommandRunner) (probed bool, err error)
	// unprobedNote is the line an install prints when gate could not ask.
	unprobedNote() string
	// stagesUnits reports whether the embedded systemd/ assets are staged.
	// The other platform's scheduler files are embedded (one binary serves
	// both) but never staged.
	stagesUnits() bool
	// wire installs or refreshes the scheduled jobs; unitAssetChanged reports
	// a staged systemd asset that changed this pass.
	wire(ctx context.Context, installer *engine, unitAssetChanged bool)
	// refreshMCPDaemon brings an already-running MCP daemon onto the build and
	// config this pass landed, after the MCP wiring.
	refreshMCPDaemon(ctx context.Context, installer *engine)
	// unwireAgents unloads and removes the launch agents an uninstall retires
	// before the agent launcher goes.
	unwireAgents(ctx context.Context, installer *engine) error
	// managerAvailable reports whether systemd --user answers, for the unit
	// retirement every uninstall runs.
	managerAvailable(ctx context.Context, installer *engine) bool
	// scheduleFiles are the installed schedule-tick files under home and the
	// render each must equal.
	scheduleFiles(home string) []scheduledFile
	// followerName is what install calls the theme follower: the launch agent
	// or its systemd units (themesync's follower).
	followerName() string
	// followerCurrent is the line a refreshed theme follower reports.
	followerCurrent() string
}

// scheduledFile is one installed scheduler file and the bytes install would
// write there.
type scheduledFile struct {
	path   string
	render func() ([]byte, error)
}

// schedulerFor is kind's scheduler.
func schedulerFor(kind servicemanager.Kind) scheduler {
	if kind == servicemanager.KindLaunchd {
		return launchdScheduler{}
	}
	return systemdScheduler{}
}

// nativeScheduler is this host's scheduler.
func nativeScheduler() scheduler { return schedulerFor(servicemanager.Native()) }

// jobScheduler is the scheduler these options install under: the one a test
// set, else this host's.
func (options Options) jobScheduler() scheduler {
	if options.scheduler != nil {
		return options.scheduler
	}
	return nativeScheduler()
}

type launchdScheduler struct{}

func (launchdScheduler) gate(ctx context.Context, runner CommandRunner) (bool, error) {
	syncRunning, syncAnswered := launchAgentRunning(ctx, runner, launchdLabel)
	if syncRunning {
		return true, ErrLaunchAgentRunning
	}
	scheduleRunning, scheduleAnswered := launchAgentRunning(ctx, runner, scheduleLaunchdLabel)
	if scheduleRunning {
		return true, ErrScheduleAgentRunning
	}
	reminderRunning, reminderAnswered := launchAgentRunning(ctx, runner, retiredScheduleLaunchdLabel)
	if reminderRunning {
		return true, ErrScheduleAgentRunning
	}
	return syncAnswered && scheduleAnswered && reminderAnswered, nil
}

func (launchdScheduler) unprobedNote() string { return launchGateUnprobedNote }

// stagesUnits is false: the launch agents are not staged either, since launchd
// refuses a symlinked plist, so wireLaunchAgent writes a real file into
// ~/Library/LaunchAgents instead of linking one out of the managed root.
func (launchdScheduler) stagesUnits() bool { return false }

func (launchdScheduler) wire(ctx context.Context, installer *engine, _ bool) {
	if installer.apply {
		if installer.options.schedulerGateUnprobed {
			installer.skip(launchGateUnprobedNote)
		} else {
			installer.ok("launch-agent gate: name-sync is not mid-execution")
		}
	}
	installer.deferFailure(installer.ensureLaunchdLogDir())
	installer.deferFailure(installer.wireLaunchAgent(ctx))
	installer.deferFailure(installer.wireScheduleLaunchAgent(ctx))
	installer.deferFailure(installer.wireMCPLaunchAgent(ctx))
	installer.deferFailure(installer.refreshThemeFollower(ctx))
}

// refreshMCPDaemon does nothing: wireMCPLaunchAgent already reloaded the
// agent.
func (launchdScheduler) refreshMCPDaemon(context.Context, *engine) {}

func (launchdScheduler) unwireAgents(ctx context.Context, installer *engine) error {
	if err := installer.unwireLaunchAgent(ctx); err != nil {
		return err
	}
	return installer.unwireScheduleLaunchAgent(ctx)
}

// managerAvailable is false without asking: a launchd host has no systemd
// user manager to drive.
func (launchdScheduler) managerAvailable(context.Context, *engine) bool { return false }

func (launchdScheduler) scheduleFiles(home string) []scheduledFile {
	return []scheduledFile{{
		path: scheduleLaunchAgentFile(home),
		render: func() ([]byte, error) {
			wanted, err := renderScheduleLaunchAgent(home)
			if err != nil {
				return nil, fmt.Errorf("render embedded asset %s: %w", scheduleLaunchdAsset, err)
			}
			return wanted, nil
		},
	}}
}

func (launchdScheduler) followerName() string { return "theme follower launch agent" }

func (launchdScheduler) followerCurrent() string {
	return "theme follower launch agent runs the stable launcher"
}

type systemdScheduler struct{}

func (systemdScheduler) gate(ctx context.Context, runner CommandRunner) (bool, error) {
	syncRunning, syncAnswered := schedulerServiceRunning(ctx, runner, "pfm-name-sync.service")
	if syncRunning {
		return true, ErrNameSyncRunning
	}
	scheduleRunning, scheduleAnswered := schedulerServiceRunning(ctx, runner, scheduleServiceUnit)
	if scheduleRunning {
		return true, ErrScheduleRunning
	}
	reminderRunning, reminderAnswered := schedulerServiceRunning(ctx, runner, retiredScheduleServiceUnit)
	if reminderRunning {
		return true, ErrScheduleRunning
	}
	return syncAnswered && scheduleAnswered && reminderAnswered, nil
}

func (systemdScheduler) unprobedNote() string { return nameSyncGateUnprobedNote }

func (systemdScheduler) stagesUnits() bool { return true }

func (systemdScheduler) wire(ctx context.Context, installer *engine, unitAssetChanged bool) {
	if installer.apply && installer.options.schedulerGateUnprobed {
		installer.skip(nameSyncGateUnprobedNote)
	}
	unitChanged, err := installer.wireUnits(ctx)
	installer.deferFailure(err)
	reload := unitAssetChanged || unitChanged
	if !reload && installer.apply {
		for _, unit := range []string{nameSyncPathUnit, nameSyncTimerUnit, scheduleTimerUnit} {
			state, err := fleetUnitState(ctx, installer.options.Runner, unit)
			if err != nil || state != unitStateActive {
				reload = true
				break
			}
		}
	}
	if reload {
		installer.reloadUnits(ctx)
	}
}

// refreshMCPDaemon restarts the enabled daemon: a host build replaces the
// binary without changing the unit file, and MCP client wiring can change
// without changing either. enable --now leaves an already-running process
// untouched, so the daemon restarts after its complete config/client
// transaction has landed. A restart that does not come back fails the run
// after every later step has landed, like a failed plugin install.
func (systemdScheduler) refreshMCPDaemon(ctx context.Context, installer *engine) {
	if installer.apply && installer.options.MCPEnabled[chatName] && installer.userManagerAvailable(ctx) {
		installer.deferFailure(installer.restartMCPUnit(ctx))
	}
}

func (systemdScheduler) unwireAgents(context.Context, *engine) error { return nil }

func (systemdScheduler) managerAvailable(ctx context.Context, installer *engine) bool {
	return installer.userManagerAvailable(ctx)
}

func (systemdScheduler) scheduleFiles(home string) []scheduledFile {
	var files []scheduledFile
	for _, name := range []string{"systemd/" + scheduleTimerUnit, "systemd/" + scheduleServiceUnit} {
		files = append(files, scheduledFile{
			path: filepath.Join(paths.SystemdUserDir(home), filepath.Base(name)),
			render: func() ([]byte, error) {
				wanted, err := readAsset(name)
				if err != nil {
					return nil, fmt.Errorf("read embedded asset %s: %w", name, err)
				}
				wanted, err = renderServiceEnv(wanted, home)
				if err != nil {
					return nil, fmt.Errorf("render embedded asset %s: %w", name, err)
				}
				return wanted, nil
			},
		})
	}
	return files
}

func (systemdScheduler) followerName() string { return "theme follower systemd units" }

func (systemdScheduler) followerCurrent() string { return "theme follower systemd units current" }
