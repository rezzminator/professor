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

// fakeScheduleSource is a scheduleSource answering from fixed lists, so the
// rendering table covers each state without a database.
type fakeScheduleSource struct {
	degraded    error
	due         []fleetdb.Schedule
	problems    []fleetdb.Schedule
	dueErr      error
	problemsErr error
}

func (f fakeScheduleSource) Degraded() error { return f.degraded }

func (f fakeScheduleSource) DueSchedules(context.Context, time.Time) ([]fleetdb.Schedule, error) {
	return f.due, f.dueErr
}

func (f fakeScheduleSource) ScheduleProblems(
	context.Context, time.Time, time.Duration,
) ([]fleetdb.Schedule, error) {
	return f.problems, f.problemsErr
}

func armedTimerReport() serviceManagerReport {
	return serviceManagerReport{
		Manager: "systemd",
		Present: true,
		Unit: serviceManagerUnitState{
			Unit: "pfm-schedule.timer", Present: true, Enabled: true, Active: true, State: "active",
		},
	}
}

func unitReport(unit serviceManagerUnitState) serviceManagerReport {
	unit.Unit = "pfm-schedule.timer"
	return serviceManagerReport{Manager: "systemd", Present: true, Unit: unit}
}

// TestRenderScheduleDoctorStates pins the one `doctor: schedules` line for each
// scheduler state and each database state, and the warning count each returns.
func TestRenderScheduleDoctorStates(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	dbErr := errors.New("disk image is malformed")
	overdue := fleetdb.Schedule{ID: 3, Label: "standup", NextFire: now.Add(-20 * time.Minute)}
	withinGrace := fleetdb.Schedule{ID: 4, Label: "tea", NextFire: now.Add(-2 * time.Minute)}
	failedOld := fleetdb.Schedule{
		ID: 7, Label: "old", NextFire: now.Add(-20 * time.Minute),
		LastError: "chat gone", LastErrorAt: now.Add(-2 * time.Hour),
	}
	failedNew := fleetdb.Schedule{
		ID: 9, Label: "new", NextFire: now.Add(-20 * time.Minute),
		LastError: "inject refused", LastErrorAt: now.Add(-time.Hour),
	}
	const head = "doctor: schedules scheduler=systemd unit=pfm-schedule.timer "
	const armed = head + "armed=true state=active "
	cases := []struct {
		name     string
		skipOn   string
		schedule serviceManagerReport
		drift    scheduleTimerDrift
		source   fakeScheduleSource
		want     string
		warnings int
	}{
		{
			name:     "drifted scheduler",
			schedule: armedTimerReport(),
			drift:    scheduleTimerDrift{Path: "/h/.config/systemd/user/pfm-schedule.timer", Drifted: true},
			want:     armed + "drift=/h/.config/systemd/user/pfm-schedule.timer due=0 overdue=0 failed=0 — run pfm install --yes\n",
			warnings: 1,
		},
		{
			name: "drifted scheduler with timer missing", schedule: unitReport(serviceManagerUnitState{}),
			drift: scheduleTimerDrift{Path: "/h/.config/systemd/user/pfm-schedule.timer", Drifted: true},
			want: head + "armed=false present=false enabled=false state=none " +
				"drift=/h/.config/systemd/user/pfm-schedule.timer due=0 overdue=0 failed=0 — run pfm install --yes\n",
			warnings: 1,
		},
		{
			name: "could not compare scheduler", schedule: armedTimerReport(),
			drift:    scheduleTimerDrift{Err: errors.New("read /h/x: is a directory")},
			want:     armed + "drift=could_not_compare error=read /h/x: is a directory due=0 overdue=0 failed=0\n",
			warnings: 1,
		},
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
				"start with: systemctl --user enable --now pfm-schedule.timer\n",
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
				Manager: "systemd", Unit: serviceManagerUnitState{Unit: "pfm-schedule.timer"},
			},
			want: "doctor: schedules scheduler=none unit=pfm-schedule.timer due=0 overdue=0 failed=0 — " +
				"no systemd user manager on this host: schedules fire only when pfm internal schedule-fire is run\n",
		},
		{
			name:     "overdue schedule",
			schedule: armedTimerReport(),
			source:   fakeScheduleSource{due: []fleetdb.Schedule{overdue}, problems: []fleetdb.Schedule{overdue}},
			want:     armed + "due=1 overdue=1 failed=0 — 1 schedule(s) due longer than 15m0s: nothing is firing them\n",
			warnings: 1,
		},
		{
			name:     "due within grace",
			schedule: armedTimerReport(),
			source:   fakeScheduleSource{due: []fleetdb.Schedule{withinGrace}},
			want:     armed + "due=1 overdue=0 failed=0\n",
		},
		{
			name:     "failed schedule",
			schedule: armedTimerReport(),
			source:   fakeScheduleSource{problems: []fleetdb.Schedule{failedOld}},
			want:     armed + "due=0 overdue=0 failed=1 — last failure: schedule 7 (old): chat gone\n",
			warnings: 1,
		},
		{
			name:     "two failures name the latest",
			schedule: armedTimerReport(),
			source:   fakeScheduleSource{problems: []fleetdb.Schedule{failedOld, failedNew}},
			want:     armed + "due=0 overdue=0 failed=2 — last failure: schedule 9 (new): inject refused\n",
			warnings: 1,
		},
		{
			name:     "overdue and failed",
			schedule: unitReport(serviceManagerUnitState{}),
			source: fakeScheduleSource{
				due: []fleetdb.Schedule{overdue}, problems: []fleetdb.Schedule{overdue, failedOld},
			},
			want: head + "armed=false present=false enabled=false state=none due=1 overdue=1 failed=1 — " +
				"run pfm install --yes; 1 schedule(s) due longer than 15m0s: nothing is firing them; " +
				"last failure: schedule 7 (old): chat gone\n",
			warnings: 1,
		},
		{
			name:     "degraded source",
			schedule: armedTimerReport(),
			source:   fakeScheduleSource{degraded: dbErr, due: []fleetdb.Schedule{overdue}},
			want: armed + "db=unreadable error=disk image is malformed — " +
				"schedule state unknown; the doctor: shared store row counts it\n",
		},
		{
			name:     "due query error",
			schedule: armedTimerReport(),
			source:   fakeScheduleSource{dueErr: dbErr, problems: []fleetdb.Schedule{overdue}},
			want:     armed + "db=unreadable error=disk image is malformed — schedule state unknown\n",
			warnings: 1,
		},
		{
			name:     "problems query error",
			schedule: armedTimerReport(),
			source:   fakeScheduleSource{problemsErr: dbErr, due: []fleetdb.Schedule{overdue}},
			want:     armed + "db=unreadable error=disk image is malformed — schedule state unknown\n",
			warnings: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipOn == goRuntime.GOOS {
				t.Skipf("%s treats a loaded StartInterval job as armed", tc.skipOn)
			}
			var out strings.Builder
			got := renderScheduleDoctor(context.Background(), &out, tc.schedule, tc.drift, tc.source, now)
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

func stubScheduleTimerProbe(t *testing.T, report serviceManagerReport) {
	t.Helper()
	previous := scheduleTimerProbeOverride
	scheduleTimerProbeOverride = func(context.Context, deps.Runner) serviceManagerReport { return report }
	t.Cleanup(func() { scheduleTimerProbeOverride = previous })
}

// TestPrintScheduleDoctorRealStateOverdue reads a real shared state database
// holding one schedule due for 20 minutes: past the 15-minute grace.
func TestPrintScheduleDoctorRealStateOverdue(t *testing.T) {
	stubScheduleTimerProbe(t, armedTimerReport())
	dir := t.TempDir()
	values := paths.Values{Home: dir, StateDB: filepath.Join(dir, "state.db")}
	now := time.Now()
	ctx := context.Background()
	store := fleetdb.OpenSharedState(ctx, values)
	if err := store.Degraded(); err != nil {
		t.Fatalf("open state db: %v", err)
	}
	if _, err := store.CreateSchedule(ctx, fleetdb.Schedule{
		SessionID: "cc-alpha", Engine: "claude", Label: "standup", Prompt: "stand up",
		Interval: time.Hour, Created: now.Add(-2 * time.Hour), NextFire: now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close state db: %v", err)
	}

	var out strings.Builder
	got := printScheduleDoctor(ctx, &out, &deps.FakeRunner{}, values, now)
	want := "doctor: schedules scheduler=systemd unit=pfm-schedule.timer armed=true state=active " +
		"drift=" + missingScheduleAsset(dir) + " due=1 overdue=1 failed=0 — run pfm install --yes; " +
		"1 schedule(s) due longer than 15m0s: nothing is firing them\n"
	if out.String() != want {
		t.Fatalf("output =\n%q\nwant\n%q", out.String(), want)
	}
	if got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}
}

// TestPrintScheduleDoctorUnreadableState puts the state database under a path
// whose parent is a regular file: the line says so and carries no counts.
func TestPrintScheduleDoctorUnreadableState(t *testing.T) {
	stubScheduleTimerProbe(t, armedTimerReport())
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	values := paths.Values{Home: dir, StateDB: filepath.Join(blocker, "state.db")}

	var out strings.Builder
	got := printScheduleDoctor(context.Background(), &out, &deps.FakeRunner{}, values, time.Now())
	line := out.String()
	if !strings.HasPrefix(line, "doctor: schedules scheduler=systemd unit=pfm-schedule.timer armed=true state=active "+
		"drift="+missingScheduleAsset(dir)+" db=unreadable error=") || !strings.HasSuffix(line,
		" — run pfm install --yes; schedule state unknown; the doctor: shared store row counts it\n") {
		t.Fatalf("output = %q", line)
	}
	if strings.Contains(line, "due=") || strings.Count(line, "\n") != 1 {
		t.Fatalf("an unreadable database must render one line without counts: %q", line)
	}
	if got != 1 {
		t.Fatalf("warnings = %d, want 1 for the missing armed schedule", got)
	}
}

func missingScheduleAsset(home string) string {
	if goRuntime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents", "com.professor.pfm.schedule.plist")
	}
	return filepath.Join(home, ".config", "systemd", "user", "pfm-schedule.timer")
}

func TestPrintScheduleDoctorScheduleDrift(t *testing.T) {
	stubScheduleTimerProbe(t, armedTimerReport())
	home := t.TempDir()
	timer := filepath.Join(home, ".config", "systemd", "user", "pfm-schedule.timer")
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.professor.pfm.schedule.plist")
	for _, path := range []string{timer, plist} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	values := paths.Values{Home: home, StateDB: filepath.Join(home, "state.db")}
	ctx := context.Background()
	store := fleetdb.OpenSharedState(ctx, values)
	if err := store.Degraded(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	path := timer
	if goRuntime.GOOS == "darwin" {
		path = plist
	}
	var out strings.Builder
	got := printScheduleDoctor(ctx, &out, &deps.FakeRunner{}, values, time.Now())
	want := "doctor: schedules scheduler=systemd unit=pfm-schedule.timer armed=true state=active drift=" +
		path + " due=0 overdue=0 failed=0 — run pfm install --yes\n"
	if out.String() != want || got != 1 {
		t.Fatalf("output=%q warnings=%d want=%q warnings=1", out.String(), got, want)
	}
}
