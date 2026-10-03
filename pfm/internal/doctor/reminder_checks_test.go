package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// fakeReminderSource is a reminderSource answering from fixed lists, so the
// rendering table covers each state without a database.
type fakeReminderSource struct {
	degraded    error
	due         []fleetdb.Reminder
	problems    []fleetdb.Reminder
	dueErr      error
	problemsErr error
}

func (f fakeReminderSource) Degraded() error { return f.degraded }

func (f fakeReminderSource) DueReminders(context.Context, time.Time) ([]fleetdb.Reminder, error) {
	return f.due, f.dueErr
}

func (f fakeReminderSource) ReminderProblems(
	context.Context, time.Time, time.Duration,
) ([]fleetdb.Reminder, error) {
	return f.problems, f.problemsErr
}

func armedTimerReport() serviceManagerReport {
	return serviceManagerReport{
		Manager: "systemd",
		Present: true,
		Unit: serviceManagerUnitState{
			Unit: "pfm-reminder.timer", Present: true, Enabled: true, Active: true, State: "active",
		},
	}
}

func unitReport(unit serviceManagerUnitState) serviceManagerReport {
	unit.Unit = "pfm-reminder.timer"
	return serviceManagerReport{Manager: "systemd", Present: true, Unit: unit}
}

// TestRenderReminderDoctorStates pins the one `doctor: reminders` line for each
// scheduler state and each database state, and the warning count each returns.
func TestRenderReminderDoctorStates(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	dbErr := errors.New("disk image is malformed")
	overdue := fleetdb.Reminder{ID: 3, Label: "standup", NextFire: now.Add(-20 * time.Minute)}
	withinGrace := fleetdb.Reminder{ID: 4, Label: "tea", NextFire: now.Add(-2 * time.Minute)}
	failedOld := fleetdb.Reminder{
		ID: 7, Label: "old", NextFire: now.Add(time.Hour), LastError: "chat gone", LastErrorAt: now.Add(-2 * time.Hour),
	}
	failedNew := fleetdb.Reminder{
		ID: 9, Label: "new", NextFire: now.Add(time.Hour),
		LastError: "inject refused", LastErrorAt: now.Add(-time.Hour),
	}
	const head = "doctor: reminders scheduler=systemd unit=pfm-reminder.timer "
	const armed = head + "armed=true state=active "
	cases := []struct {
		name     string
		skipOn   string
		schedule serviceManagerReport
		source   fakeReminderSource
		want     string
		warnings int
	}{
		{
			name:     "healthy",
			schedule: armedTimerReport(),
			want:     armed + "due=0 overdue=0 failed=0\n",
		},
		{
			name:     "timer missing",
			schedule: unitReport(serviceManagerUnitState{}),
			want:     head + "armed=false present=false enabled=false state=none due=0 overdue=0 failed=0 — run pfm install --yes\n",
			warnings: 1,
		},
		{
			name:     "timer present but inactive",
			skipOn:   "darwin",
			schedule: unitReport(serviceManagerUnitState{Present: true, Enabled: true, State: "inactive"}),
			want: head + "armed=false present=true enabled=true state=inactive due=0 overdue=0 failed=0 — " +
				"start with: systemctl --user enable --now pfm-reminder.timer\n",
			warnings: 1,
		},
		{
			name:     "could not ask",
			schedule: unitReport(serviceManagerUnitState{Err: errors.New("bus unreachable")}),
			want:     head + "could_not_ask error=bus unreachable due=0 overdue=0 failed=0\n",
			warnings: 1,
		},
		{
			name: "no manager",
			schedule: serviceManagerReport{
				Manager: "systemd", Unit: serviceManagerUnitState{Unit: "pfm-reminder.timer"},
			},
			want: "doctor: reminders scheduler=none unit=pfm-reminder.timer due=0 overdue=0 failed=0 — " +
				"no systemd user manager on this host: reminders fire only when pfm internal reminder-fire is run\n",
		},
		{
			name:     "overdue reminder",
			schedule: armedTimerReport(),
			source:   fakeReminderSource{due: []fleetdb.Reminder{overdue}, problems: []fleetdb.Reminder{overdue}},
			want:     armed + "due=1 overdue=1 failed=0 — 1 reminder(s) due longer than 15m0s: nothing is firing them\n",
			warnings: 1,
		},
		{
			name:     "due within grace",
			schedule: armedTimerReport(),
			source:   fakeReminderSource{due: []fleetdb.Reminder{withinGrace}},
			want:     armed + "due=1 overdue=0 failed=0\n",
		},
		{
			name:     "failed reminder",
			schedule: armedTimerReport(),
			source:   fakeReminderSource{problems: []fleetdb.Reminder{failedOld}},
			want:     armed + "due=0 overdue=0 failed=1 — last failure: reminder 7 (old): chat gone\n",
			warnings: 1,
		},
		{
			name:     "two failures name the latest",
			schedule: armedTimerReport(),
			source:   fakeReminderSource{problems: []fleetdb.Reminder{failedOld, failedNew}},
			want:     armed + "due=0 overdue=0 failed=2 — last failure: reminder 9 (new): inject refused\n",
			warnings: 1,
		},
		{
			name:     "overdue and failed",
			schedule: unitReport(serviceManagerUnitState{}),
			source: fakeReminderSource{
				due: []fleetdb.Reminder{overdue}, problems: []fleetdb.Reminder{overdue, failedOld},
			},
			want: head + "armed=false present=false enabled=false state=none due=1 overdue=1 failed=1 — " +
				"run pfm install --yes; 1 reminder(s) due longer than 15m0s: nothing is firing them; " +
				"last failure: reminder 7 (old): chat gone\n",
			warnings: 1,
		},
		{
			name:     "degraded source",
			schedule: armedTimerReport(),
			source:   fakeReminderSource{degraded: dbErr, due: []fleetdb.Reminder{overdue}},
			want:     armed + "db=unreadable error=disk image is malformed — reminder state unknown\n",
			warnings: 1,
		},
		{
			name:     "due query error",
			schedule: armedTimerReport(),
			source:   fakeReminderSource{dueErr: dbErr, problems: []fleetdb.Reminder{overdue}},
			want:     armed + "db=unreadable error=disk image is malformed — reminder state unknown\n",
			warnings: 1,
		},
		{
			name:     "problems query error",
			schedule: armedTimerReport(),
			source:   fakeReminderSource{problemsErr: dbErr, due: []fleetdb.Reminder{overdue}},
			want:     armed + "db=unreadable error=disk image is malformed — reminder state unknown\n",
			warnings: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipOn == goRuntime.GOOS {
				t.Skipf("%s treats a loaded StartInterval job as armed", tc.skipOn)
			}
			var out strings.Builder
			got := renderReminderDoctor(context.Background(), &out, tc.schedule, tc.source, now)
			if out.String() != tc.want {
				t.Fatalf("line =\n%q\nwant\n%q", out.String(), tc.want)
			}
			if got != tc.warnings {
				t.Fatalf("warnings = %d, want %d", got, tc.warnings)
			}
			if strings.Contains(tc.want, "db=unreadable") && strings.Contains(out.String(), "due=") {
				t.Fatalf("an unreadable database rendered counts: %q", out.String())
			}
		})
	}
}

func stubReminderScheduleProbe(t *testing.T, report serviceManagerReport) {
	t.Helper()
	previous := reminderScheduleProbeOverride
	reminderScheduleProbeOverride = func(context.Context, deps.Runner) serviceManagerReport { return report }
	t.Cleanup(func() { reminderScheduleProbeOverride = previous })
}

// TestPrintReminderDoctorRealStateOverdue reads a real shared state database
// holding one reminder due for 20 minutes: past the 15-minute grace.
func TestPrintReminderDoctorRealStateOverdue(t *testing.T) {
	stubReminderScheduleProbe(t, armedTimerReport())
	dir := t.TempDir()
	values := paths.Values{Home: dir, StateDB: filepath.Join(dir, "state.db")}
	now := time.Now()
	ctx := context.Background()
	store := fleetdb.OpenSharedState(ctx, values)
	if err := store.Degraded(); err != nil {
		t.Fatalf("open state db: %v", err)
	}
	if _, err := store.CreateReminder(ctx, fleetdb.Reminder{
		SessionID: "cc-alpha", Engine: "claude", Label: "standup", Prompt: "stand up",
		Interval: time.Hour, Created: now.Add(-2 * time.Hour), NextFire: now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("create reminder: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close state db: %v", err)
	}

	var out strings.Builder
	got := printReminderDoctor(ctx, &out, &deps.FakeRunner{}, values, now)
	want := "doctor: reminders scheduler=systemd unit=pfm-reminder.timer armed=true state=active " +
		"due=1 overdue=1 failed=0 — 1 reminder(s) due longer than 15m0s: nothing is firing them\n"
	if out.String() != want {
		t.Fatalf("output =\n%q\nwant\n%q", out.String(), want)
	}
	if got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}
}

// TestPrintReminderDoctorUnreadableState puts the state database under a path
// whose parent is a regular file: the line says so and carries no counts.
func TestPrintReminderDoctorUnreadableState(t *testing.T) {
	stubReminderScheduleProbe(t, armedTimerReport())
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	values := paths.Values{Home: dir, StateDB: filepath.Join(blocker, "state.db")}

	var out strings.Builder
	got := printReminderDoctor(context.Background(), &out, &deps.FakeRunner{}, values, time.Now())
	line := out.String()
	if !strings.HasPrefix(line, "doctor: reminders scheduler=systemd unit=pfm-reminder.timer armed=true state=active "+
		"db=unreadable error=") || !strings.HasSuffix(line, " — reminder state unknown\n") {
		t.Fatalf("output = %q", line)
	}
	if strings.Contains(line, "due=") || strings.Count(line, "\n") != 1 {
		t.Fatalf("an unreadable database must render one line without counts: %q", line)
	}
	if got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}
}
