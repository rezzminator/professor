// Package schedule owns the recurring and one-time alarms behind `pfm chat
// schedule` and the scheduler's `pfm internal schedule-fire`: the interval and time grammar, the
// message a fire types into a chat, the locked fire loop over the shared
// schedule store (fleetdb), the set/ls/rm command and the deliverer that wakes
// a chat (resuming a dead one) and injects the message.
package schedule

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

const (
	day           = 24 * time.Hour
	acceptedUse   = "weekly, <N>d, or a Go duration like 90m, minimum 1m"
	acceptedTimes = "HH:MM (its next occurrence), YYYY-MM-DD HH:MM, or RFC 3339, at least 1m ahead"
	clockLayout   = "15:04"
)

// dateLayouts are the dated forms ParseAt reads, in local time unless the form
// carries its own zone.
var dateLayouts = []string{"2006-01-02 15:04", "2006-01-02T15:04", time.RFC3339}

// ParseInterval reads a recurrence: "weekly", "<N>d" for a positive whole
// number of days, or a Go duration. It refuses anything shorter than
// fleetdb.MinScheduleInterval, naming the minimum, and anything it cannot
// read, naming the accepted forms.
func ParseInterval(text string) (time.Duration, error) {
	cleaned := strings.ToLower(strings.TrimSpace(text))
	invalid := func(cause error) error {
		if cause == nil {
			return fmt.Errorf("invalid interval %q: use %s", text, acceptedUse)
		}
		return fmt.Errorf("invalid interval %q (%w): use %s", text, cause, acceptedUse)
	}
	var interval time.Duration
	switch {
	case cleaned == "weekly":
		interval = 7 * day
	case strings.HasSuffix(cleaned, "d") && wholeNumber(strings.TrimSuffix(cleaned, "d")):
		days, err := strconv.ParseInt(strings.TrimSuffix(cleaned, "d"), 10, 64)
		if err != nil || days <= 0 || days > math.MaxInt64/int64(day) {
			return 0, invalid(err)
		}
		interval = time.Duration(days) * day
	default:
		parsed, err := time.ParseDuration(cleaned)
		if err != nil || parsed < 0 {
			return 0, invalid(err)
		}
		interval = parsed
	}
	if interval < fleetdb.MinScheduleInterval {
		minutes := int(fleetdb.MinScheduleInterval / time.Minute)
		return 0, fmt.Errorf("interval %q is too short: minimum %dm", text, minutes)
	}
	return interval, nil
}

// ParseAt reads a one-time fire time relative to now: "HH:MM" is its next
// occurrence (today, or tomorrow once today's has passed), a dated form is that
// moment. It refuses a time less than fleetdb.MinScheduleInterval ahead, and
// anything it cannot read, naming the accepted forms.
func ParseAt(text string, now time.Time) (time.Time, error) {
	cleaned := strings.TrimSpace(text)
	var fireAt time.Time
	if clock, err := time.ParseInLocation(clockLayout, cleaned, now.Location()); err == nil {
		fireAt = time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
		if !fireAt.After(now) {
			fireAt = fireAt.AddDate(0, 0, 1)
		}
	} else {
		parsed := false
		for _, layout := range dateLayouts {
			if moment, err := time.ParseInLocation(layout, cleaned, now.Location()); err == nil {
				fireAt, parsed = moment, true
				break
			}
		}
		if !parsed {
			return time.Time{}, fmt.Errorf("invalid time %q: use %s", text, acceptedTimes)
		}
	}
	if fireAt.Sub(now) < fleetdb.MinScheduleInterval {
		minutes := int(fleetdb.MinScheduleInterval / time.Minute)
		return time.Time{}, fmt.Errorf("time %q is less than %dm from now", text, minutes)
	}
	return fireAt, nil
}

func wholeNumber(text string) bool {
	if text == "" {
		return false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// FormatInterval renders an interval the way ParseInterval reads it: whole
// days as "<N>d", anything else as a Go duration.
func FormatInterval(interval time.Duration) string {
	if interval > 0 && interval%day == 0 {
		return strconv.FormatInt(int64(interval/day), 10) + "d"
	}
	return interval.String()
}

// Message is the line a fire types into the scheduled chat.
func Message(r fleetdb.Schedule) string {
	when := "every " + FormatInterval(r.Interval)
	if r.Once {
		when = "once"
	}
	return "⏰ pfm schedule " + strconv.FormatInt(r.ID, 10) + " (" + when + "): " + r.Prompt
}
