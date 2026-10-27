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
	"github.com/rezzminator/professor/pfm/internal/obs"
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

// DeliveryWarning is a failure that follows a delivery which already happened:
// Fire counts the schedule fired and carries the failure in Report.Warned.
type DeliveryWarning struct{ Err error }

func (warning *DeliveryWarning) Error() string { return warning.Err.Error() }

func (warning *DeliveryWarning) Unwrap() error { return warning.Err }

// Report is what one Fire did. Skipped means another fire held the lock and
// this one touched nothing.
type Report struct {
	Skipped bool
	Fired   []fleetdb.Schedule
	Failed  []Failure
	// Warned lists fired schedules whose delivery left a failure behind; a
	// warned delivery that could not be rescheduled is in Failed, carrying both.
	Warned []Failure
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
	ctx = context.WithValue(ctx, fireTimeKey{}, fireTiming{tick: now, started: obs.Clock(ctx).Now()})
	for index := range due {
		r := due[index]
		err := deliverer.Deliver(ctx, r, Message(r))
		var warning *DeliveryWarning
		if errors.As(err, &warning) {
			err = nil
		}
		if err != nil {
			obs.Logger(ctx).
				ErrorContext(ctx, fmt.Sprintf("schedule %d stays due: %v", r.ID, err), obs.FieldChat, r.SessionID)
			failure := err
			if _, recordErr := store.RecordScheduleFailure(ctx, r.ID, now, err.Error()); recordErr != nil {
				failure = errors.Join(err, fmt.Errorf("record failure of schedule %d: %w", r.ID, recordErr))
			}
			report.Failed = append(report.Failed, Failure{Schedule: r, Err: failure})
			continue
		}
		if _, err := store.MarkScheduleFired(ctx, r.ID, now); err != nil {
			failure := fmt.Errorf("schedule %d was delivered but could not be rescheduled: %w", r.ID, err)
			if warning != nil {
				failure = fmt.Errorf("%w; %w", failure, warning.Err)
			}
			report.Failed = append(report.Failed, Failure{Schedule: r, Err: failure})
			continue
		}
		if cleaner, ok := deliverer.(interface{ ForgetDelivery(fleetdb.Schedule) error }); ok {
			if err := cleaner.ForgetDelivery(r); err != nil {
				if warning == nil {
					warning = &DeliveryWarning{Err: err}
				} else {
					warning.Err = errors.Join(warning.Err, err)
				}
			}
		}
		report.Fired = append(report.Fired, r)
		if warning != nil {
			report.Warned = append(report.Warned, Failure{Schedule: r, Err: warning.Err})
		}
	}
	return report, nil
}
