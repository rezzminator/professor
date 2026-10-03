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

func newTestReminder(session string, created time.Time, interval time.Duration) Reminder {
	return Reminder{
		SessionID: session, Engine: "claude", Label: "worker", Prompt: "check the build",
		Interval: interval, Created: created, SetByID: "setter-id", SetByLabel: "setter",
	}
}

func TestReminderLifecycle(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0)

	weekly, err := state.CreateReminder(ctx, newTestReminder("s-1", base, 7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	hourly, err := state.CreateReminder(ctx, newTestReminder("s-2", base, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	all, err := state.Reminders(ctx)
	if err != nil || len(all) != 2 || all[0].ID != hourly || all[1].ID != weekly {
		t.Fatalf("Reminders() = %+v, %v; want hourly then weekly", all, err)
	}
	got, found, err := state.Reminder(ctx, weekly)
	if err != nil || !found {
		t.Fatalf("Reminder(%d) = found %v, %v", weekly, found, err)
	}
	want := newTestReminder("s-1", base, 7*24*time.Hour)
	want.ID = weekly
	want.NextFire = base.Add(7 * 24 * time.Hour)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reminder() = %+v\nwant %+v", got, want)
	}

	due, err := state.DueReminders(ctx, base.Add(time.Hour-time.Nanosecond))
	if err != nil || len(due) != 0 {
		t.Fatalf("DueReminders(before) = %+v, %v; want none", due, err)
	}
	now := base.Add(3 * time.Hour)
	due, err = state.DueReminders(ctx, now)
	if err != nil || len(due) != 1 || due[0].ID != hourly {
		t.Fatalf("DueReminders(now) = %+v, %v; want hourly", due, err)
	}

	if changed, err := state.RecordReminderFailure(ctx, hourly, now, "chat not found"); err != nil || !changed {
		t.Fatalf("RecordReminderFailure() = %v, %v", changed, err)
	}
	due, err = state.DueReminders(ctx, now)
	if err != nil || len(due) != 1 || due[0].LastError != "chat not found" || !due[0].LastErrorAt.Equal(now) {
		t.Fatalf("failed reminder must stay due with its error: %+v, %v", due, err)
	}

	// Three intervals were missed; one fire reschedules from now, no burst.
	if changed, err := state.MarkReminderFired(ctx, hourly, now); err != nil || !changed {
		t.Fatalf("MarkReminderFired() = %v, %v", changed, err)
	}
	got, _, err = state.Reminder(ctx, hourly)
	if err != nil || !got.Unseen || !got.LastFired.Equal(now) || !got.NextFire.Equal(now.Add(time.Hour)) ||
		got.LastError != "" || !got.LastErrorAt.IsZero() {
		t.Fatalf("fired reminder = %+v, %v", got, err)
	}
	if due, err = state.DueReminders(ctx, now); err != nil || len(due) != 0 {
		t.Fatalf("DueReminders after fire = %+v, %v; want none", due, err)
	}

	unseen, err := state.UnseenReminderSessionIDs(ctx)
	if err != nil || !reflect.DeepEqual(unseen, map[string]bool{"s-2": true}) {
		t.Fatalf("UnseenReminderSessionIDs() = %v, %v", unseen, err)
	}
	if changed, err := state.ClearReminderUnseen(ctx, "s-2"); err != nil || !changed {
		t.Fatalf("ClearReminderUnseen() = %v, %v", changed, err)
	}
	if unseen, err = state.UnseenReminderSessionIDs(ctx); err != nil || len(unseen) != 0 {
		t.Fatalf("UnseenReminderSessionIDs() after clear = %v, %v", unseen, err)
	}

	if changed, err := state.RekeyReminders(ctx, "s-1", "s-1b"); err != nil || !changed {
		t.Fatalf("RekeyReminders() = %v, %v", changed, err)
	}
	if got, _, err = state.Reminder(ctx, weekly); err != nil || got.SessionID != "s-1b" {
		t.Fatalf("rekeyed reminder = %+v, %v", got, err)
	}
	if changed, err := state.RekeyReminders(ctx, "absent", "other"); err != nil || changed {
		t.Fatalf("RekeyReminders(absent) = %v, %v; want unchanged", changed, err)
	}

	if removed, err := state.RemoveReminder(ctx, weekly); err != nil || !removed {
		t.Fatalf("RemoveReminder() = %v, %v", removed, err)
	}
	if removed, err := state.RemoveReminder(ctx, weekly); err != nil || removed {
		t.Fatalf("RemoveReminder(again) = %v, %v; want not removed", removed, err)
	}
	if _, found, err := state.Reminder(ctx, weekly); err != nil || found {
		t.Fatalf("Reminder(removed) found=%v err=%v", found, err)
	}
}

func TestCreateReminderRefusesInvalidInput(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	base := time.Unix(1_800_000_000, 0)
	for name, mutate := range map[string]func(*Reminder){
		"below minimum": func(r *Reminder) { r.Interval = MinReminderInterval - time.Second },
		"empty prompt":  func(r *Reminder) { r.Prompt = "  " },
		"empty session": func(r *Reminder) { r.SessionID = "" },
		"empty engine":  func(r *Reminder) { r.Engine = "" },
		"zero created":  func(r *Reminder) { r.Created = time.Time{} },
	} {
		reminder := newTestReminder("s-1", base, time.Hour)
		mutate(&reminder)
		if _, err := state.CreateReminder(context.Background(), reminder); !errors.Is(err, ErrInvalidReminder) {
			t.Errorf("%s: CreateReminder() error = %v, want ErrInvalidReminder", name, err)
		}
	}
	first := newTestReminder("s-1", base, MinReminderInterval)
	if _, err := state.CreateReminder(context.Background(), first); err != nil {
		t.Fatalf("CreateReminder(minimum) = %v", err)
	}
}

func TestReminderProblemsNamesFailedAndOverdue(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0)
	failed, _ := state.CreateReminder(ctx, newTestReminder("failed", base, 24*time.Hour))
	overdue, _ := state.CreateReminder(ctx, newTestReminder("overdue", base, time.Hour))
	if _, err := state.CreateReminder(ctx, newTestReminder("healthy", base, 7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	now := base.Add(3 * time.Hour)
	if _, err := state.RecordReminderFailure(ctx, failed, now, "resume refused"); err != nil {
		t.Fatal(err)
	}
	problems, err := state.ReminderProblems(ctx, now, time.Hour)
	if err != nil || len(problems) != 2 || problems[0].ID != overdue || problems[1].ID != failed {
		t.Fatalf("ReminderProblems() = %+v, %v; want overdue then failed", problems, err)
	}
}

func TestReminderOperationsReportADegradedStore(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(context.Background(), paths.Values{StateDB: filepath.Join(blocker, "pfm.db")})
	if state.Degraded() == nil {
		t.Fatal("store under a file opened cleanly")
	}
	if _, err := state.Reminders(context.Background()); err == nil {
		t.Fatal("Reminders() on a degraded store returned no error")
	}
	if _, err := state.UnseenReminderSessionIDs(context.Background()); err == nil {
		t.Fatal("UnseenReminderSessionIDs() on a degraded store returned no error")
	}
	if _, err := state.MarkReminderFired(context.Background(), 1, time.Now()); err == nil {
		t.Fatal("MarkReminderFired() on a degraded store returned no error")
	}
}

func TestSharedMigrationFromV2AddsRemindersAndBacksUp(t *testing.T) {
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
	if _, err := state.CreateReminder(ctx, newTestReminder("s", time.Unix(1, 0), time.Hour)); err != nil {
		t.Fatalf("CreateReminder() after migration = %v", err)
	}
	if _, err := os.Stat(path + ".bak-before-v3"); err != nil {
		t.Fatalf("v2 database was not backed up before v3: %v", err)
	}
}
