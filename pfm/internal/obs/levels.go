package obs

import (
	"fmt"
	"log/slog"
	"strings"
)

// Level is the activity log's record level: slog's, plus LevelOff above every
// record. One parser (ParseLevel) reads every spelling of one — pfm.config.json's
// log.level and log.components, PFM_LOG_LEVEL and PFM_LOG_COMPONENTS, and `pfm
// log --level` — so no surface can accept a value another refuses.
type Level = slog.Level

// LevelOff records nothing: it sits above ERROR, so no record can reach it. As
// the global level it also writes no file (OpenLog).
const LevelOff Level = slog.LevelError + 100

// levelTable is the one list of accepted spellings, in order: `warning` is an
// alias of `warn` (the canonical spelling comes first), `off` the fifth level
// that records nothing.
var levelTable = []struct {
	name  string
	level Level
}{
	{"debug", slog.LevelDebug},
	{"info", slog.LevelInfo},
	{"warn", slog.LevelWarn},
	{"warning", slog.LevelWarn},
	{"error", slog.LevelError},
	{"off", LevelOff},
}

// LevelNames are the accepted spellings, in levelTable's order.
var LevelNames = func() []string {
	names := make([]string, 0, len(levelTable))
	for _, entry := range levelTable {
		names = append(names, entry.name)
	}
	return names
}()

// ParseLevel reads one level spelling, case-insensitively and ignoring
// surrounding space. A value that is not one of LevelNames is an error that
// names every accepted spelling — the caller adds which setting stays in force.
func ParseLevel(text string) (Level, error) {
	name := strings.ToLower(strings.TrimSpace(text))
	for _, entry := range levelTable {
		if entry.name == name {
			return entry.level, nil
		}
	}
	return slog.LevelInfo, fmt.Errorf("level %q is not one of %s", text, strings.Join(LevelNames, ", "))
}

// LevelName is the canonical spelling of level: the highest named level that
// does not exceed it (so a level between two names reads as the lower one),
// and the first spelling where two share a level (warn, never warning).
func LevelName(level Level) string {
	name, floor := levelTable[0].name, Level(-1<<31)
	for _, entry := range levelTable {
		if entry.level <= level && entry.level > floor {
			name, floor = entry.name, entry.level
		}
	}
	return name
}
