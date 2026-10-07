package reminder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Store is the slice of the shared reminder store a fire reads and writes;
// *fleetdb.Store satisfies it.
type Store interface {
	DueReminders(ctx context.Context, now time.Time) ([]fleetdb.Reminder, error)
	MarkReminderFired(ctx context.Context, id int64, now time.Time) (bool, error)
	RecordReminderFailure(ctx context.Context, id int64, now time.Time, failure string) (bool, error)
}

// Deliverer types one reminder message into its chat, waking the chat first
// when it is not running.
type Deliverer interface {
	Deliver(ctx context.Context, r fleetdb.Reminder, message string) error
}

// Failure is one reminder a fire could not deliver or could not record.
type Failure struct {
	Reminder fleetdb.Reminder
	Err      error
}

// Report is what one Fire did. Skipped means another fire held the lock and
// this one touched nothing.
type Report struct {
	Skipped bool
	Fired   []fleetdb.Reminder
	Failed  []Failure
}

// FireLockPath is the fire lock, beside the shared state database.
func FireLockPath(values paths.Values) string {
	return filepath.Join(filepath.Dir(values.StateDB), "reminder-fire.lock")
}

func openLock(lockPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create reminder lock directory for %s: %w", lockPath, err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open reminder lock %s: %w", lockPath, err)
	}
	return file, nil
}

// Fire delivers every reminder due at now, once each. Two overlapping ticks
// never both deliver: the second finds the lock held and reports Skipped. A
// delivery that fails is recorded on its reminder, which stays due; the loop
// goes on to the next one. A reminder that missed several intervals fires once
// and is rescheduled one interval from now.
func Fire(
	ctx context.Context,
	store Store,
	deliverer Deliverer,
	lockPath string,
	now time.Time,
) (report Report, returnErr error) {
	lock, err := openLock(lockPath)
	if err != nil {
		return Report{}, err
	}
	defer func() {
		// Closing the descriptor releases the flock; the file itself is kept so
		// a concurrent opener never races an unlink.
		if closeErr := lock.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close reminder lock %s: %w", lockPath, closeErr))
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return Report{Skipped: true}, nil
		}
		return Report{}, fmt.Errorf("lock reminder fire %s: %w", lockPath, err)
	}
	due, err := store.DueReminders(ctx, now)
	if err != nil {
		return Report{}, fmt.Errorf("list due reminders: %w", err)
	}
	for index := range due {
		r := due[index]
		if err := deliverer.Deliver(ctx, r, Message(r)); err != nil {
			failure := err
			if _, recordErr := store.RecordReminderFailure(ctx, r.ID, now, err.Error()); recordErr != nil {
				failure = errors.Join(err, fmt.Errorf("record failure of reminder %d: %w", r.ID, recordErr))
			}
			report.Failed = append(report.Failed, Failure{Reminder: r, Err: failure})
			continue
		}
		if _, err := store.MarkReminderFired(ctx, r.ID, now); err != nil {
			report.Failed = append(report.Failed, Failure{
				Reminder: r,
				Err:      fmt.Errorf("reminder %d was delivered but could not be rescheduled: %w", r.ID, err),
			})
			continue
		}
		report.Fired = append(report.Fired, r)
	}
	return report, nil
}
