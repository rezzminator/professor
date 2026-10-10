package schedule

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type delivery struct {
	id      int64
	message string
}

type fakeDeliverer struct {
	failing   map[int64]error
	delivered []delivery
}

func (fake *fakeDeliverer) Deliver(_ context.Context, r fleetdb.Schedule, message string) error {
	if err := fake.failing[r.ID]; err != nil {
		return err
	}
	fake.delivered = append(fake.delivered, delivery{id: r.ID, message: message})
	return nil
}

func openStore(t *testing.T) *fleetdb.Store {
	t.Helper()
	ctx := context.Background()
	store := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
	if err := store.Degraded(); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func seed(t *testing.T, store *fleetdb.Store, label string, interval time.Duration, created time.Time) int64 {
	t.Helper()
	id, err := store.CreateSchedule(context.Background(), fleetdb.Schedule{
		SessionID: "sess-" + label, Engine: "claude", Label: label, Prompt: "ping " + label,
		Interval: interval, Created: created,
	})
	if err != nil {
		t.Fatalf("seed schedule %s: %v", label, err)
	}
	return id
}

func mustSchedule(t *testing.T, store *fleetdb.Store, id int64) fleetdb.Schedule {
	t.Helper()
	got, found, err := store.Schedule(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("read schedule %d: found=%v err=%v", id, found, err)
	}
	return got
}

func TestFireDeliversDueScheduleOnceAndReschedules(t *testing.T) {
	store := openStore(t)
	now := time.Unix(1_800_000_000, 0)
	due := seed(t, store, "due", time.Hour, now.Add(-90*time.Minute))
	later := seed(t, store, "later", time.Hour, now.Add(-10*time.Minute))
	deliverer := &fakeDeliverer{}

	report, err := Fire(context.Background(), store, deliverer, filepath.Join(t.TempDir(), "fire.lock"), now)
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if report.Skipped || len(report.Failed) != 0 || len(report.Fired) != 1 || report.Fired[0].ID != due {
		t.Fatalf("report = %+v, want exactly schedule %d fired", report, due)
	}
	if len(deliverer.delivered) != 1 || deliverer.delivered[0].id != due ||
		deliverer.delivered[0].message != Message(report.Fired[0]) {
		t.Fatalf("deliveries = %+v, want one carrying Message(due)", deliverer.delivered)
	}
	got := mustSchedule(t, store, due)
	if !got.NextFire.Equal(now.Add(time.Hour)) || !got.Unseen || !got.LastFired.Equal(now) {
		t.Fatalf("fired schedule = %+v, want next now+1h, unseen, last_fired now", got)
	}
	untouched := mustSchedule(t, store, later)
	wantNext := now.Add(-10 * time.Minute).Add(time.Hour)
	if untouched.Unseen || !untouched.LastFired.IsZero() || !untouched.NextFire.Equal(wantNext) {
		t.Fatalf("not-yet-due schedule changed: %+v", untouched)
	}
}

func TestFireMissedIntervalsFireOnceWithoutBurst(t *testing.T) {
	store := openStore(t)
	now := time.Unix(1_800_000_000, 0)
	id := seed(t, store, "missed", time.Hour, now.Add(-3*time.Hour-time.Minute))
	deliverer := &fakeDeliverer{}

	lock := filepath.Join(t.TempDir(), "fire.lock")
	if _, err := Fire(context.Background(), store, deliverer, lock, now); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(deliverer.delivered) != 1 {
		t.Fatalf("three missed intervals delivered %d times, want exactly 1", len(deliverer.delivered))
	}
	if got := mustSchedule(t, store, id); !got.NextFire.Equal(now.Add(time.Hour)) {
		t.Fatalf("next fire = %v, want now+interval %v", got.NextFire, now.Add(time.Hour))
	}
}

func TestFireDeliverErrorKeepsScheduleDueAndOthersStillFire(t *testing.T) {
	store := openStore(t)
	now := time.Unix(1_800_000_000, 0)
	created := now.Add(-2 * time.Hour)
	broken := seed(t, store, "broken", time.Hour, created)
	healthy := seed(t, store, "healthy", time.Hour, created)
	deliverer := &fakeDeliverer{failing: map[int64]error{broken: errors.New("tmux server vanished")}}

	report, err := Fire(context.Background(), store, deliverer, filepath.Join(t.TempDir(), "fire.lock"), now)
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(report.Failed) != 1 || report.Failed[0].Schedule.ID != broken ||
		!strings.Contains(report.Failed[0].Err.Error(), "tmux server vanished") {
		t.Fatalf("Failed = %+v, want schedule %d with the deliver error", report.Failed, broken)
	}
	if len(report.Fired) != 1 || report.Fired[0].ID != healthy {
		t.Fatalf("Fired = %+v, want the healthy schedule %d", report.Fired, healthy)
	}
	stuck := mustSchedule(t, store, broken)
	if !strings.Contains(stuck.LastError, "tmux server vanished") || !stuck.NextFire.Before(now.Add(time.Second)) {
		t.Fatalf("failed schedule = %+v, want LastError set and still due", stuck)
	}
	due, err := store.DueSchedules(context.Background(), now)
	if err != nil || len(due) != 1 || due[0].ID != broken {
		t.Fatalf("due after fire = %+v, %v; want only the failed schedule", due, err)
	}
}

func TestFireSkipsWhenAnotherFireHoldsTheLock(t *testing.T) {
	store := openStore(t)
	now := time.Unix(1_800_000_000, 0)
	seed(t, store, "held", time.Hour, now.Add(-2*time.Hour))
	deliverer := &fakeDeliverer{}
	lock := filepath.Join(t.TempDir(), "fire.lock")

	holder, err := openLock(lock)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold lock: %v", err)
	}

	report, err := Fire(context.Background(), store, deliverer, lock, now)
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if !report.Skipped || len(deliverer.delivered) != 0 || len(report.Fired) != 0 {
		t.Fatalf("report=%+v deliveries=%+v, want skipped with nothing delivered", report, deliverer.delivered)
	}
}

func TestFireLockPathSitsBesideTheStateDatabase(t *testing.T) {
	t.Parallel()
	got := FireLockPath(paths.Values{StateDB: "/state/pfm/pfm.db"})
	if got != "/state/pfm/schedule-fire.lock" {
		t.Fatalf("LockPath = %q", got)
	}
}
