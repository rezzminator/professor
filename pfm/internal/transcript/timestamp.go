package transcript

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// isoDate is the ISO date-time grammar Date.parse reads, plus the space
// separator V8 also accepts.
var isoDate = regexp.MustCompile(`^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?` +
	`(?:[T ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?(Z|[+-]\d{2}:?\d{2})?)?$`)

// ParseTimestamp is the one timestamp parser for transcript stamps, in the
// grammar JavaScript's Date.parse reads: a Z or offset reads as given, a
// date-time without one in loc (time.UTC reads it as UTC, as Python's
// fromisoformat readers did), a bare date as UTC; a day past the month's end
// rolls over as V8 rolls it; a fraction keeps its microseconds, as Python's
// fromisoformat does (Date.parse's milliseconds are the caller's cut). ok is
// false for any other text.
func ParseTimestamp(text string, loc *time.Location) (time.Time, bool) {
	match := isoDate.FindStringSubmatch(text)
	if match == nil {
		return time.Time{}, false
	}
	field := func(index, fallback int) int {
		if match[index] == "" {
			return fallback
		}
		value, _ := strconv.Atoi(match[index])
		return value
	}
	year, month, day := field(1, 0), field(2, 1), field(3, 1)
	hour, minute, second := field(4, 0), field(5, 0), field(6, 0)
	micros := 0
	if fraction := match[7]; fraction != "" {
		micros, _ = strconv.Atoi((fraction + "00000")[:6])
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || minute > 59 || second > 59 ||
		hour > 24 || (hour == 24 && minute+second+micros > 0) {
		return time.Time{}, false
	}
	zone := time.UTC
	switch offset := match[8]; {
	case offset == "Z":
	case offset != "":
		digits := strings.ReplaceAll(offset[1:], ":", "")
		hours, _ := strconv.Atoi(digits[:2])
		minutes, _ := strconv.Atoi(digits[2:])
		seconds := (hours*60 + minutes) * 60
		if offset[0] == '-' {
			seconds = -seconds
		}
		zone = time.FixedZone("", seconds)
	case match[4] != "":
		zone = loc
	}
	at := time.Date(year, time.Month(month), day, hour, minute, second, micros*int(time.Microsecond), zone)
	return at, true
}
