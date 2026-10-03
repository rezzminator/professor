package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// reminderDoctorGrace is three 5-minute ticks of pfm-reminder.timer. One missed
// tick (a suspend, a slow fire, a tick that found the fire lock held) is
// normal, but a reminder due three ticks running means nothing is firing.
const reminderDoctorGrace = 15 * time.Minute

// reminderScheduleProbeOverride lets tests exercise printReminderDoctor
// without a real service manager, as ServiceManagerProbeOverride does for the
// MCP row. Production leaves it nil.
var reminderScheduleProbeOverride func(context.Context, deps.Runner) serviceManagerReport

func configuredReminderScheduleProbe(ctx context.Context, runner deps.Runner) serviceManagerReport {
	if reminderScheduleProbeOverride != nil {
		return reminderScheduleProbeOverride(ctx, runner)
	}
	return probeReminderSchedule(ctx, runner)
}

// reminderSource is the part of *fleetdb.Store the reminders row reads.
type reminderSource interface {
	Degraded() error
	DueReminders(context.Context, time.Time) ([]fleetdb.Reminder, error)
	ReminderProblems(context.Context, time.Time, time.Duration) ([]fleetdb.Reminder, error)
}

// printSupervisionDoctor prints the two supervision rows: the MCP daemon and
// the reminder schedule. It returns their warnings.
func printSupervisionDoctor(
	ctx context.Context, stdout io.Writer, dependencies Dependencies, runtime config.Runtime,
) int {
	return printServiceManagerDoctor(ctx, stdout, dependencies.Runner, runtime) +
		printReminderDoctor(ctx, stdout, dependencies.Runner, runtime.Paths, dependencies.Clock.Now())
}

// printReminderDoctor opens the shared state database read-only (never
// creating or migrating it; this row only queries it), prints the one
// reminders line and returns its warnings.
func printReminderDoctor(
	ctx context.Context, stdout io.Writer, runner deps.Runner, values paths.Values, now time.Time,
) (warnings int) {
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	store := fleetdb.OpenSharedStateReadOnly(ctx, values)
	defer func() {
		if err := store.Close(); err != nil {
			fmt.Fprintf(stdout, "doctor: reminders close state db=%s error=%v\n", values.StateDB, err)
			warnings++
		}
	}()
	return renderReminderDoctor(ctx, stdout, configuredReminderScheduleProbe(ctx, runner), store, now)
}

// renderReminderDoctor writes the one `doctor: reminders` line: the scheduler
// part, the database part and the hints for what needs doing. It returns 1 when
// any problem holds. An unreadable database renders as db=unreadable with no
// counts — a failure to look never reads as zero reminders.
func renderReminderDoctor(
	ctx context.Context, stdout io.Writer, schedule serviceManagerReport, source reminderSource, now time.Time,
) int {
	manager, unit := schedule.Manager, schedule.Unit.Unit
	if unit == "" {
		manager, unit = reminderScheduleIdentity()
	}
	problem := false
	var hints []string
	var scheduler string
	switch {
	case !schedule.Present:
		scheduler = fmt.Sprintf("scheduler=none unit=%s", unit)
		hints = append(hints, fmt.Sprintf(
			"no %s user manager on this host: reminders fire only when pfm internal reminder-fire is run", manager,
		))
	case schedule.Unit.Err != nil:
		scheduler = fmt.Sprintf("scheduler=%s unit=%s could_not_ask error=%v", manager, unit, schedule.Unit.Err)
		problem = true
	case reminderScheduleArmed(schedule.Unit):
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

	database, dbHints, dbProblem := reminderDatabasePart(ctx, source, now)
	hints = append(hints, dbHints...)
	line := "doctor: reminders " + scheduler + " " + database
	if len(hints) > 0 {
		line += " — " + strings.Join(hints, "; ")
	}
	fmt.Fprintln(stdout, line)
	if problem || dbProblem {
		return 1
	}
	return 0
}

// reminderDatabasePart renders the database half of the line, its hints and
// whether it holds a problem.
func reminderDatabasePart(ctx context.Context, source reminderSource, now time.Time) (string, []string, bool) {
	unreadable := func(err error) (string, []string, bool) {
		return fmt.Sprintf("db=unreadable error=%v", err), []string{"reminder state unknown"}, true
	}
	if err := source.Degraded(); errors.Is(err, fleetdb.ErrAbsent) {
		// Not created yet: no reminder was ever set, and nothing is wrong.
		return "db=absent", nil, false
	} else if err != nil {
		return unreadable(err)
	}
	due, err := source.DueReminders(ctx, now)
	if err != nil {
		return unreadable(err)
	}
	problems, err := source.ReminderProblems(ctx, now, reminderDoctorGrace)
	if err != nil {
		return unreadable(err)
	}
	overdue, failed := 0, 0
	var latest *fleetdb.Reminder
	for i := range problems {
		reminder := &problems[i]
		if !reminder.NextFire.After(now.Add(-reminderDoctorGrace)) {
			overdue++
		}
		if reminder.LastError == "" {
			continue
		}
		failed++
		if latest == nil || reminder.LastErrorAt.After(latest.LastErrorAt) {
			latest = reminder
		}
	}
	var hints []string
	if overdue > 0 {
		hints = append(hints, fmt.Sprintf(
			"%d reminder(s) due longer than %s: nothing is firing them", overdue, reminderDoctorGrace,
		))
	}
	if latest != nil {
		hints = append(hints, fmt.Sprintf(
			"last failure: reminder %d (%s): %s", latest.ID, latest.Label, latest.LastError,
		))
	}
	return fmt.Sprintf("due=%d overdue=%d failed=%d", len(due), overdue, failed), hints, len(hints) > 0
}
