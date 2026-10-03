// Package reminder owns the recurring alarms behind `pfm chat reminder` and
// the scheduler's `pfm internal reminder-fire`: the interval grammar, the
// message a fire types into a chat, the locked fire loop over the shared
// reminder store (fleetdb), the set/ls/rm command and the deliverer that wakes
// a chat (resuming a dead one) and injects the message.
package reminder

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

const (
	day         = 24 * time.Hour
	acceptedUse = "weekly, <N>d, or a Go duration like 90m, minimum 1m"
)

// ParseInterval reads a recurrence: "weekly", "<N>d" for a positive whole
// number of days, or a Go duration. It refuses anything shorter than
// fleetdb.MinReminderInterval, naming the minimum, and anything it cannot
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
	if interval < fleetdb.MinReminderInterval {
		minutes := int(fleetdb.MinReminderInterval / time.Minute)
		return 0, fmt.Errorf("interval %q is too short: minimum %dm", text, minutes)
	}
	return interval, nil
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

// Message is the line a fire types into the reminded chat.
func Message(r fleetdb.Reminder) string {
	return "⏰ pfm reminder " + strconv.FormatInt(r.ID, 10) + " (every " + FormatInterval(r.Interval) + "): " + r.Prompt
}
