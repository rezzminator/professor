package schedule

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

// Store is the slice of the shared schedule store a fire reads and writes;
// *fleetdb.Store satisfies it.
type Store interface {
	DueSchedules(ctx context.Context, now time.Time) ([]fleetdb.Schedule, error)
	MarkScheduleFired(ctx context.Context, id int64, now time.Time) (bool, error)
	RecordScheduleFailure(ctx context.Context, id int64, now time.Time, failure string) (bool, error)
}

// Deliverer types one schedule message into its chat, waking the chat first
// when it is not running.
type Deliverer interface {
	Deliver(ctx context.Context, r fleetdb.Schedule, message string) error
}

// Failure is one schedule a fire could not deliver or could not record.
type Failure struct {
	Schedule fleetdb.Schedule
	Err      error
}

// Report is what one Fire did. Skipped means another fire held the lock and
// this one touched nothing.
type Report struct {
	Skipped bool
	Fired   []fleetdb.Schedule
	Failed  []Failure
}

// FireLockPath is the fire lock, beside the shared state database.
func FireLockPath(values paths.Values) string {
	return filepath.Join(filepath.Dir(values.StateDB), "schedule-fire.lock")
}

func openLock(lockPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create schedule lock directory for %s: %w", lockPath, err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open schedule lock %s: %w", lockPath, err)
	}
	return file, nil
}

// Fire delivers every schedule due at now, once each. Two overlapping ticks
// never both deliver: the second finds the lock held and reports Skipped. A
// delivery that fails is recorded on its schedule, which stays due; the loop
// goes on to the next one. A schedule that missed several intervals fires once
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
			returnErr = errors.Join(returnErr, fmt.Errorf("close schedule lock %s: %w", lockPath, closeErr))
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return Report{Skipped: true}, nil
		}
		return Report{}, fmt.Errorf("lock schedule fire %s: %w", lockPath, err)
	}
	due, err := store.DueSchedules(ctx, now)
	if err != nil {
		return Report{}, fmt.Errorf("list due schedules: %w", err)
	}
	for index := range due {
		r := due[index]
		if err := deliverer.Deliver(ctx, r, Message(r)); err != nil {
			failure := err
			if _, recordErr := store.RecordScheduleFailure(ctx, r.ID, now, err.Error()); recordErr != nil {
				failure = errors.Join(err, fmt.Errorf("record failure of schedule %d: %w", r.ID, recordErr))
			}
			report.Failed = append(report.Failed, Failure{Schedule: r, Err: failure})
			continue
		}
		if _, err := store.MarkScheduleFired(ctx, r.ID, now); err != nil {
			report.Failed = append(report.Failed, Failure{
				Schedule: r,
				Err:      fmt.Errorf("schedule %d was delivered but could not be rescheduled: %w", r.ID, err),
			})
			continue
		}
		report.Fired = append(report.Fired, r)
	}
	return report, nil
}
