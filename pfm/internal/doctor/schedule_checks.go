package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// scheduleDoctorGrace is three 5-minute ticks of pfm-schedule.timer. One missed
// tick (a suspend, a slow fire, a tick that found the fire lock held) is
// normal, but a schedule due three ticks running means nothing is firing.
const scheduleDoctorGrace = 15 * time.Minute

type scheduleTimerDrift struct {
	Path    string
	Drifted bool
	Err     error
}

// scheduleTimerProbeOverride lets tests exercise printScheduleDoctor
// without a real service manager, as ServiceManagerProbeOverride does for the
// MCP row. Production leaves it nil.
var scheduleTimerProbeOverride func(context.Context, deps.Runner) serviceManagerReport

func configuredScheduleTimerProbe(ctx context.Context, runner deps.Runner) serviceManagerReport {
	if scheduleTimerProbeOverride != nil {
		return scheduleTimerProbeOverride(ctx, runner)
	}
	return probeScheduleTimer(ctx, runner)
}

// scheduleSource is the part of *fleetdb.Store the schedules row reads.
type scheduleSource interface {
	Degraded() error
	DueSchedules(context.Context, time.Time) ([]fleetdb.Schedule, error)
	ScheduleProblems(context.Context, time.Time, time.Duration) ([]fleetdb.Schedule, error)
}

// printSupervisionDoctor prints the supervision rows: the MCP daemon, the
// schedule timer and the launch agents' launcher. It returns their warnings.
func printSupervisionDoctor(
	ctx context.Context, stdout io.Writer, dependencies Dependencies, runtime config.Runtime,
) int {
	return printServiceManagerDoctor(ctx, stdout, dependencies.Runner, runtime) +
		printScheduleDoctor(ctx, stdout, dependencies.Runner, runtime.Paths, dependencies.Clock.Now()) +
		printLaunchAgentsDoctor(stdout, runtime.Paths.Home)
}

// printScheduleDoctor opens the shared state database read-only (never
// creating or migrating it; this row only queries it), prints the one
// schedules line and returns its warnings.
func printScheduleDoctor(
	ctx context.Context, stdout io.Writer, runner deps.Runner, values paths.Values, now time.Time,
) (warnings int) {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	store := fleetdb.OpenSharedStateReadOnly(ctx, values)
	defer func() {
		if err := store.Close(); err != nil {
			fmt.Fprintf(stdout, "doctor: schedules close state db=%s error=%v\n", values.StateDB, err)
			warnings++
		}
	}()
	schedule := configuredScheduleTimerProbe(ctx, runner)
	var drift scheduleTimerDrift
	drift.Path, drift.Drifted, drift.Err = installer.ScheduleTimerDrift(
		values.Home,
		scheduleTimerArmed(schedule.Unit),
	)
	return renderScheduleDoctor(ctx, stdout, schedule, drift, store, now)
}

// renderScheduleDoctor writes the one `doctor: schedules` line: the scheduler
// part, the database part and the hints for what needs doing. It returns 1 when
// any problem holds. An unreadable database renders as db=unreadable with no
// counts — a failure to look never reads as zero schedules.
func renderScheduleDoctor(
	ctx context.Context,
	stdout io.Writer,
	schedule serviceManagerReport,
	drift scheduleTimerDrift,
	source scheduleSource,
	now time.Time,
) int {
	manager, unit := schedule.Manager, schedule.Unit.Unit
	if unit == "" {
		manager, unit = scheduleTimerIdentity()
	}
	problem := false
	var hints []string
	var scheduler string
	switch {
	case !schedule.Present:
		scheduler = fmt.Sprintf("scheduler=none unit=%s", unit)
		hints = append(hints, fmt.Sprintf(
			"no %s user manager on this host: schedules fire only when pfm internal schedule-fire is run", manager,
		))
	case schedule.Unit.Err != nil:
		scheduler = fmt.Sprintf("scheduler=%s unit=%s could_not_ask error=%v", manager, unit, schedule.Unit.Err)
		problem = true
	case scheduleTimerArmed(schedule.Unit):
		scheduler = fmt.Sprintf("scheduler=%s unit=%s armed=true state=%s", manager, unit, schedule.Unit.State)
	default:
		state := schedule.Unit.State
		if state == "" {
			state = "none"
		}
		scheduler = fmt.Sprintf(
			"scheduler=%s unit=%s armed=false present=%t enabled=%t state=%s",
			manager, unit, schedule.Unit.Present, schedule.Unit.Enabled, state,
		)
		problem = true
		if schedule.Unit.Present {
			hints = append(hints, "start with: "+serviceManagerStartHint(manager, unit))
		} else {
			hints = append(hints, "run pfm install --yes")
		}
	}

	if drift.Err != nil {
		scheduler += fmt.Sprintf(" drift=could_not_compare error=%v", drift.Err)
		problem = true
	} else if drift.Drifted {
		scheduler += " drift=" + drift.Path
		problem = true
		if !slices.Contains(hints, "run pfm install --yes") {
			hints = append(hints, "run pfm install --yes")
		}
	}

	database, dbHints, dbProblem := scheduleDatabasePart(ctx, source, now)
	hints = append(hints, dbHints...)
	line := "doctor: schedules " + scheduler + " " + database
	if len(hints) > 0 {
		line += " — " + strings.Join(hints, "; ")
	}
	fmt.Fprintln(stdout, line)
	if problem || dbProblem {
		return 1
	}
	return 0
}

// scheduleDatabasePart renders the database half of the line, its hints and
// whether it holds a problem.
func scheduleDatabasePart(ctx context.Context, source scheduleSource, now time.Time) (string, []string, bool) {
	unreadable := func(err error) (string, []string, bool) {
		return fmt.Sprintf("db=unreadable error=%v", err), []string{"schedule state unknown"}, true
	}
	if err := source.Degraded(); errors.Is(err, fleetdb.ErrAbsent) {
		// Not created yet: no schedule was ever set, and nothing is wrong.
		return "db=absent", nil, false
	} else if err != nil {
		return fmt.Sprintf("db=unreadable error=%v", err),
			[]string{"schedule state unknown; the doctor: shared store row counts it"}, false
	}
	due, err := source.DueSchedules(ctx, now)
	if err != nil {
		return unreadable(err)
	}
	problems, err := source.ScheduleProblems(ctx, now, scheduleDoctorGrace)
	if err != nil {
		return unreadable(err)
	}
	overdue, failed := 0, 0
	var latest *fleetdb.Schedule
	for i := range problems {
		schedule := &problems[i]
		if schedule.LastError == "" && !schedule.NextFire.After(now.Add(-scheduleDoctorGrace)) {
			overdue++
		}
		if schedule.LastError == "" {
			continue
		}
		failed++
		if latest == nil || schedule.LastErrorAt.After(latest.LastErrorAt) {
			latest = schedule
		}
	}
	var hints []string
	if overdue > 0 {
		hints = append(hints, fmt.Sprintf(
			"%d schedule(s) due longer than %s: nothing is firing them", overdue, scheduleDoctorGrace,
		))
	}
	if latest != nil {
		hints = append(hints, fmt.Sprintf(
			"last failure: schedule %d (%s): %s", latest.ID, latest.Label, latest.LastError,
		))
	}
	return fmt.Sprintf("due=%d overdue=%d failed=%d", len(due), overdue, failed), hints, len(hints) > 0
}
