package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func newTestSchedule(session string, created time.Time, interval time.Duration) Schedule {
	return Schedule{
		SessionID: session, Engine: "claude", Label: "worker", Prompt: "check the build",
		Interval: interval, Created: created, SetByID: "setter-id", SetByLabel: "setter",
	}
}

func TestScheduleLifecycle(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0)

	weekly, err := state.CreateSchedule(ctx, newTestSchedule("s-1", base, 7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	hourly, err := state.CreateSchedule(ctx, newTestSchedule("s-2", base, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	all, err := state.Schedules(ctx)
	if err != nil || len(all) != 2 || all[0].ID != hourly || all[1].ID != weekly {
		t.Fatalf("Schedules() = %+v, %v; want hourly then weekly", all, err)
	}
	got, found, err := state.Schedule(ctx, weekly)
	if err != nil || !found {
		t.Fatalf("Schedule(%d) = found %v, %v", weekly, found, err)
	}
	want := newTestSchedule("s-1", base, 7*24*time.Hour)
	want.ID = weekly
	want.NextFire = base.Add(7 * 24 * time.Hour)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Schedule() = %+v\nwant %+v", got, want)
	}

	due, err := state.DueSchedules(ctx, base.Add(time.Hour-time.Nanosecond))
	if err != nil || len(due) != 0 {
		t.Fatalf("DueSchedules(before) = %+v, %v; want none", due, err)
	}
	now := base.Add(3 * time.Hour)
	due, err = state.DueSchedules(ctx, now)
	if err != nil || len(due) != 1 || due[0].ID != hourly {
		t.Fatalf("DueSchedules(now) = %+v, %v; want hourly", due, err)
	}

	if changed, err := state.RecordScheduleFailure(ctx, hourly, now, "chat not found"); err != nil || !changed {
		t.Fatalf("RecordScheduleFailure() = %v, %v", changed, err)
	}
	due, err = state.DueSchedules(ctx, now)
	if err != nil || len(due) != 1 || due[0].LastError != "chat not found" || !due[0].LastErrorAt.Equal(now) {
		t.Fatalf("failed schedule must stay due with its error: %+v, %v", due, err)
	}

	// Three intervals were missed; one fire reschedules from now, no burst.
	if changed, err := state.MarkScheduleFired(ctx, hourly, now); err != nil || !changed {
		t.Fatalf("MarkScheduleFired() = %v, %v", changed, err)
	}
	got, _, err = state.Schedule(ctx, hourly)
	if err != nil || !got.Unseen || !got.LastFired.Equal(now) || !got.NextFire.Equal(now.Add(time.Hour)) ||
		got.LastError != "" || !got.LastErrorAt.IsZero() {
		t.Fatalf("fired schedule = %+v, %v", got, err)
	}
	if due, err = state.DueSchedules(ctx, now); err != nil || len(due) != 0 {
		t.Fatalf("DueSchedules after fire = %+v, %v; want none", due, err)
	}

	unseen, err := state.UnseenScheduleSessionIDs(ctx)
	if err != nil || !reflect.DeepEqual(unseen, map[string]bool{"s-2": true}) {
		t.Fatalf("UnseenScheduleSessionIDs() = %v, %v", unseen, err)
	}
	if changed, err := state.ClearScheduleUnseen(ctx, "s-2"); err != nil || !changed {
		t.Fatalf("ClearScheduleUnseen() = %v, %v", changed, err)
	}
	if unseen, err = state.UnseenScheduleSessionIDs(ctx); err != nil || len(unseen) != 0 {
		t.Fatalf("UnseenScheduleSessionIDs() after clear = %v, %v", unseen, err)
	}

	if changed, err := state.RekeySchedules(ctx, "s-1", "s-1b"); err != nil || !changed {
		t.Fatalf("RekeySchedules() = %v, %v", changed, err)
	}
	if got, _, err = state.Schedule(ctx, weekly); err != nil || got.SessionID != "s-1b" {
		t.Fatalf("rekeyed schedule = %+v, %v", got, err)
	}
	if changed, err := state.RekeySchedules(ctx, "absent", "other"); err != nil || changed {
		t.Fatalf("RekeySchedules(absent) = %v, %v; want unchanged", changed, err)
	}

	if removed, err := state.RemoveSchedule(ctx, weekly); err != nil || !removed {
		t.Fatalf("RemoveSchedule() = %v, %v", removed, err)
	}
	if removed, err := state.RemoveSchedule(ctx, weekly); err != nil || removed {
		t.Fatalf("RemoveSchedule(again) = %v, %v; want not removed", removed, err)
	}
	if _, found, err := state.Schedule(ctx, weekly); err != nil || found {
		t.Fatalf("Schedule(removed) found=%v err=%v", found, err)
	}
}

func TestCreateScheduleRefusesInvalidInput(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	base := time.Unix(1_800_000_000, 0)
	for name, mutate := range map[string]func(*Schedule){
		"below minimum": func(r *Schedule) { r.Interval = MinScheduleInterval - time.Second },
		"empty prompt":  func(r *Schedule) { r.Prompt = "  " },
		"empty session": func(r *Schedule) { r.SessionID = "" },
		"empty engine":  func(r *Schedule) { r.Engine = "" },
		"zero created":  func(r *Schedule) { r.Created = time.Time{} },
	} {
		schedule := newTestSchedule("s-1", base, time.Hour)
		mutate(&schedule)
		if _, err := state.CreateSchedule(context.Background(), schedule); !errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("%s: CreateSchedule() error = %v, want ErrInvalidSchedule", name, err)
		}
	}
	first := newTestSchedule("s-1", base, MinScheduleInterval)
	if _, err := state.CreateSchedule(context.Background(), first); err != nil {
		t.Fatalf("CreateSchedule(minimum) = %v", err)
	}
}

func TestScheduleProblemsNamesFailedAndOverdue(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0)
	failed, _ := state.CreateSchedule(ctx, newTestSchedule("failed", base, 24*time.Hour))
	overdue, _ := state.CreateSchedule(ctx, newTestSchedule("overdue", base, time.Hour))
	if _, err := state.CreateSchedule(ctx, newTestSchedule("healthy", base, 7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	now := base.Add(3 * time.Hour)
	if _, err := state.RecordScheduleFailure(ctx, failed, now, "resume refused"); err != nil {
		t.Fatal(err)
	}
	problems, err := state.ScheduleProblems(ctx, now, time.Hour)
	if err != nil || len(problems) != 2 || problems[0].ID != overdue || problems[1].ID != failed {
		t.Fatalf("ScheduleProblems() = %+v, %v; want overdue then failed", problems, err)
	}
}

func TestScheduleOperationsReportADegradedStore(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(context.Background(), paths.Values{StateDB: filepath.Join(blocker, "pfm.db")})
	if state.Degraded() == nil {
		t.Fatal("store under a file opened cleanly")
	}
	if _, err := state.Schedules(context.Background()); err == nil {
		t.Fatal("Schedules() on a degraded store returned no error")
	}
	if _, err := state.UnseenScheduleSessionIDs(context.Background()); err == nil {
		t.Fatal("UnseenScheduleSessionIDs() on a degraded store returned no error")
	}
	if _, err := state.MarkScheduleFired(context.Background(), 1, time.Now()); err == nil {
		t.Fatal("MarkScheduleFired() on a degraded store returned no error")
	}
}

func TestSharedMigrationFromV2AddsSchedulesAndBacksUp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "pfm.db")
	ctx := context.Background()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL;"+schemaDDL+migrationV2+
		`INSERT INTO launch VALUES('keep','claude',1,0,5,5); PRAGMA user_version=2;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(ctx, paths.Values{StateDB: path})
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	var version, count int
	if err := state.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	keepRow := state.db.QueryRow("SELECT count(*) FROM launch WHERE session_id='keep'")
	if err := keepRow.Scan(&count); err != nil || count != 1 {
		t.Fatalf("launch row=%d err=%v", count, err)
	}
	if _, err := state.CreateSchedule(ctx, newTestSchedule("s", time.Unix(1, 0), time.Hour)); err != nil {
		t.Fatalf("CreateSchedule() after migration = %v", err)
	}
	if _, err := os.Stat(path + ".bak-before-v3"); err != nil {
		t.Fatalf("v2 database was not backed up before v3: %v", err)
	}
}

// TestSharedOpenRenamesARemindersTableToSchedules: a version-3 file an older pfm
// left keeps its rows under the schedules name, with the schedules indexes, at
// the version a version-3 pfm still opens.
func TestSharedOpenRenamesARemindersTableToSchedules(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pfm.db")
	ctx := context.Background()
	seedSharedState(
		t,
		path,
		v3Schema+`INSERT INTO reminders(session_id,engine,prompt,interval_ns,next_fire_ns,created_ns)
VALUES('kept','claude','check the build',3600000000000,5,1);`,
	)
	state := OpenSharedState(ctx, paths.Values{StateDB: path})
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	all, err := state.Schedules(ctx)
	if err != nil || len(all) != 1 || all[0].SessionID != "kept" || all[0].Prompt != "check the build" {
		t.Fatalf("Schedules() after the rename = %+v, %v; want the kept row", all, err)
	}
	var version int
	if err := state.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("user_version=%d err=%v, want %d", version, err, SchemaVersion)
	}
	rows, err := state.db.Query(
		"SELECT name FROM sqlite_master WHERE name LIKE 'reminders%' OR name LIKE 'schedules%' ORDER BY name",
	)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"schedules", "schedules_next_fire", "schedules_session"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("schema objects = %v, want %v", names, want)
	}
}
