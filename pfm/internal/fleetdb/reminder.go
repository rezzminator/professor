package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MinReminderInterval is the shortest recurrence a reminder may carry; the
// table's CHECK holds the same floor so no writer can slip under it.
const MinReminderInterval = time.Minute

// ErrInvalidReminder marks a reminder refused at entry, before any write.
var ErrInvalidReminder = errors.New("invalid reminder")

// Reminder is one recurring alarm that wakes a chat. Times are wall clock;
// a zero LastFired or LastErrorAt means "never". LastError is the most recent
// failed fire, cleared by the next successful one: a reminder that could not
// be delivered stays due and says why, it is never silently dropped.
type Reminder struct {
	ID          int64         `json:"id"`
	SessionID   string        `json:"session_id"`
	Engine      string        `json:"engine"`
	Label       string        `json:"label"`
	Prompt      string        `json:"prompt"`
	Interval    time.Duration `json:"interval_ns"`
	NextFire    time.Time     `json:"next_fire"`
	Created     time.Time     `json:"created"`
	LastFired   time.Time     `json:"last_fired"`
	Unseen      bool          `json:"unseen"`
	SetByID     string        `json:"set_by_id"`
	SetByLabel  string        `json:"set_by_label"`
	LastError   string        `json:"last_error"`
	LastErrorAt time.Time     `json:"last_error_at"`
}

const reminderColumns = `id,session_id,engine,label,prompt,interval_ns,next_fire_ns,created_ns,
last_fired_ns,unseen,set_by_id,set_by_label,last_error,last_error_ns`

func validateReminder(reminder Reminder) error {
	switch {
	case strings.TrimSpace(reminder.SessionID) == "":
		return fmt.Errorf("%w: empty session id", ErrInvalidReminder)
	case strings.TrimSpace(reminder.Engine) == "":
		return fmt.Errorf("%w: empty engine", ErrInvalidReminder)
	case strings.TrimSpace(reminder.Prompt) == "":
		return fmt.Errorf("%w: empty prompt", ErrInvalidReminder)
	case reminder.Interval < MinReminderInterval:
		return fmt.Errorf(
			"%w: interval %s is below the %s minimum", ErrInvalidReminder, reminder.Interval, MinReminderInterval,
		)
	case reminder.Created.IsZero():
		return fmt.Errorf("%w: zero creation time", ErrInvalidReminder)
	}
	return nil
}

// CreateReminder stores a new reminder and returns its id. A zero NextFire
// means Created+Interval; LastFired, Unseen and LastError start empty.
func (s *Store) CreateReminder(ctx context.Context, reminder Reminder) (int64, error) {
	if s.db == nil {
		return 0, fmt.Errorf("create reminder for %q: %w", reminder.SessionID, s.degraded)
	}
	if err := validateReminder(reminder); err != nil {
		return 0, err
	}
	next := reminder.NextFire
	if next.IsZero() {
		next = reminder.Created.Add(reminder.Interval)
	}
	result, err := s.exec(ctx, `
INSERT INTO reminders(session_id,engine,label,prompt,interval_ns,next_fire_ns,created_ns,set_by_id,set_by_label)
VALUES(?,?,?,?,?,?,?,?,?)`,
		reminder.SessionID, reminder.Engine, reminder.Label, reminder.Prompt,
		int64(reminder.Interval), next.UnixNano(), reminder.Created.UnixNano(),
		reminder.SetByID, reminder.SetByLabel)
	if err != nil {
		return 0, fmt.Errorf("create reminder for %q: %w", reminder.SessionID, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read new reminder id for %q: %w", reminder.SessionID, err)
	}
	return id, nil
}

// Reminders lists every reminder, soonest first.
func (s *Store) Reminders(ctx context.Context) ([]Reminder, error) {
	return s.selectReminders(ctx, "list reminders", "ORDER BY next_fire_ns,id")
}

// DueReminders lists the reminders whose next fire is at or before now,
// soonest first. A reminder whose last fire failed is still due.
func (s *Store) DueReminders(ctx context.Context, now time.Time) ([]Reminder, error) {
	return s.selectReminders(
		ctx, "list due reminders", "WHERE next_fire_ns<=? ORDER BY next_fire_ns,id", now.UnixNano(),
	)
}

// ReminderProblems is the doctor's view: every reminder whose last fire
// failed, or that has been due for longer than grace (no scheduler fired it).
func (s *Store) ReminderProblems(ctx context.Context, now time.Time, grace time.Duration) ([]Reminder, error) {
	return s.selectReminders(
		ctx,
		"list reminder problems",
		"WHERE last_error<>'' OR next_fire_ns<=? ORDER BY next_fire_ns,id",
		now.Add(-grace).UnixNano(),
	)
}

// Reminder reads one reminder; found is false when no row has that id.
func (s *Store) Reminder(ctx context.Context, id int64) (Reminder, bool, error) {
	reminders, err := s.selectReminders(ctx, fmt.Sprintf("read reminder %d", id), "WHERE id=?", id)
	if err != nil {
		return Reminder{}, false, err
	}
	if len(reminders) == 0 {
		return Reminder{}, false, nil
	}
	return reminders[0], true, nil
}

func (s *Store) selectReminders(
	ctx context.Context,
	what, clause string,
	args ...any,
) (result []Reminder, returnErr error) {
	if s.db == nil {
		return nil, fmt.Errorf("%s: %w", what, s.degraded)
	}
	rows, err := s.query(ctx, "SELECT "+reminderColumns+" FROM reminders "+clause, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("%s: close rows: %w", what, err))
		}
	}()
	for rows.Next() {
		var reminder Reminder
		var interval, next, created, fired, errorAt int64
		var unseen int
		if err := rows.Scan(
			&reminder.ID, &reminder.SessionID, &reminder.Engine, &reminder.Label, &reminder.Prompt,
			&interval, &next, &created, &fired, &unseen,
			&reminder.SetByID, &reminder.SetByLabel, &reminder.LastError, &errorAt,
		); err != nil {
			return nil, fmt.Errorf("%s: scan: %w", what, err)
		}
		reminder.Interval = time.Duration(interval)
		reminder.NextFire = time.Unix(0, next)
		reminder.Created = time.Unix(0, created)
		reminder.LastFired = unixNanoOrZero(fired)
		reminder.LastErrorAt = unixNanoOrZero(errorAt)
		reminder.Unseen = unseen == 1
		result = append(result, reminder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return result, nil
}

func unixNanoOrZero(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value)
}

// RemoveReminder deletes one reminder; removed is false when no row had that id.
func (s *Store) RemoveReminder(ctx context.Context, id int64) (bool, error) {
	return s.changeReminders(ctx, fmt.Sprintf("remove reminder %d", id), "DELETE FROM reminders WHERE id=?", id)
}

// MarkReminderFired records a delivered fire: last_fired=now, unseen=1, the
// failure cleared, and the next fire one interval from now — a reminder that
// missed several intervals while the machine was down fires once, not in a burst.
func (s *Store) MarkReminderFired(ctx context.Context, id int64, now time.Time) (bool, error) {
	return s.changeReminders(ctx, fmt.Sprintf("mark reminder %d fired", id), `
UPDATE reminders SET last_fired_ns=?, unseen=1, next_fire_ns=?+interval_ns, last_error='', last_error_ns=0
WHERE id=?`, now.UnixNano(), now.UnixNano(), id)
}

// RecordReminderFailure records why a fire could not be delivered. The
// reminder keeps its next fire, so it stays due and the next tick retries it.
func (s *Store) RecordReminderFailure(ctx context.Context, id int64, now time.Time, failure string) (bool, error) {
	if strings.TrimSpace(failure) == "" {
		return false, fmt.Errorf("%w: reminder %d failure has no reason", ErrInvalidReminder, id)
	}
	return s.changeReminders(ctx, fmt.Sprintf("record reminder %d failure", id),
		"UPDATE reminders SET last_error=?, last_error_ns=? WHERE id=?", failure, now.UnixNano(), id)
}

// ClearReminderUnseen marks every reminder of one chat seen, reporting
// whether any row changed.
func (s *Store) ClearReminderUnseen(ctx context.Context, sessionID string) (bool, error) {
	return s.changeReminders(ctx, fmt.Sprintf("clear unseen reminders of %q", sessionID),
		"UPDATE reminders SET unseen=0 WHERE session_id=? AND unseen=1", sessionID)
}

// RekeyReminders moves every reminder of oldSessionID to newSessionID — a
// reload that continues a chat under a new session id keeps its reminders.
func (s *Store) RekeyReminders(ctx context.Context, oldSessionID, newSessionID string) (bool, error) {
	if strings.TrimSpace(oldSessionID) == "" || strings.TrimSpace(newSessionID) == "" {
		return false, fmt.Errorf(
			"%w: rekey %q to %q needs both session ids", ErrInvalidReminder, oldSessionID, newSessionID,
		)
	}
	return s.changeReminders(ctx, fmt.Sprintf("rekey reminders %q to %q", oldSessionID, newSessionID),
		"UPDATE reminders SET session_id=? WHERE session_id=?", newSessionID, oldSessionID)
}

func (s *Store) changeReminders(ctx context.Context, what, statement string, args ...any) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("%s: %w", what, s.degraded)
	}
	result, err := s.exec(ctx, statement, args...)
	if err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%s: count rows: %w", what, err)
	}
	return affected > 0, nil
}

// UnseenReminderSessionIDs is the set of chats with a fired reminder nobody
// has looked at yet.
func (s *Store) UnseenReminderSessionIDs(ctx context.Context) (result map[string]bool, returnErr error) {
	if s.db == nil {
		return nil, fmt.Errorf("list unseen reminder chats: %w", s.degraded)
	}
	rows, err := s.query(ctx, "SELECT DISTINCT session_id FROM reminders WHERE unseen=1")
	if err != nil {
		return nil, fmt.Errorf("list unseen reminder chats: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("list unseen reminder chats: close rows: %w", err))
		}
	}()
	result = map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list unseen reminder chats: scan: %w", err)
		}
		result[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list unseen reminder chats: %w", err)
	}
	return result, nil
}
