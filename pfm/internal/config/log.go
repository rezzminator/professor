package config

import (
	"fmt"
	"strings"

	"hostops/pfm/internal/obs"
)

// Log is the activity log's machine policy: the level internal/obs writes at
// when the build's own default is not wanted, and the size-capped rotation it
// keeps per pfm home (docs/dev/trains/testing-foundation/waves/6-activity-log/
// spec.md § Destinations and environments).
//
// Level is empty when the file says nothing, which means "let the build
// decide": a -alpha VERSION logs at debug, a release at info.
type Log struct {
	Level     string
	KeepFiles int
	MaxMB     int
}

// LogLevels are the level names log.level accepts.
var LogLevels = []string{"debug", "info", "warn", "error"}

// DefaultLog is the activity log policy when the file says nothing.
func DefaultLog() Log {
	// internal/obs owns the rotation and falls back to the same two numbers
	// for a zero-valued Log, so there is exactly one default per knob.
	return Log{KeepFiles: obs.DefaultKeepFiles, MaxMB: obs.DefaultMaxMB}
}

type rawLog struct {
	Level     *string `json:"level,omitempty"`
	KeepFiles *int    `json:"keepFiles,omitempty"`
	MaxMB     *int    `json:"maxMB,omitempty"`
}

// applyLog overlays the log keys the file actually set onto result, the way
// applyCompactNudge overlays its own: a bad value is a refused config, never a
// silently substituted default, because a log nobody can size is a log nobody
// reads.
func applyLog(result *Config, raw *rawLog) error {
	if raw == nil {
		return nil
	}
	if raw.Level != nil {
		level := strings.ToLower(strings.TrimSpace(*raw.Level))
		if !containsLogLevel(level) {
			return fmt.Errorf(
				"config %s: log.level must be one of %s, got %q",
				result.Path,
				strings.Join(LogLevels, ", "),
				*raw.Level,
			)
		}
		result.Log.Level = level
		result.Sources["log.level"] = SourceFile
	}
	if raw.KeepFiles != nil {
		if *raw.KeepFiles < 1 || *raw.KeepFiles > 64 {
			return fmt.Errorf(
				"config %s: log.keepFiles must be 1..64 (files kept per pfm home), got %d",
				result.Path,
				*raw.KeepFiles,
			)
		}
		result.Log.KeepFiles = *raw.KeepFiles
		result.Sources["log.keepFiles"] = SourceFile
	}
	if raw.MaxMB != nil {
		if *raw.MaxMB < 1 || *raw.MaxMB > 1024 {
			return fmt.Errorf(
				"config %s: log.maxMB must be 1..1024 (megabytes per file), got %d",
				result.Path,
				*raw.MaxMB,
			)
		}
		result.Log.MaxMB = *raw.MaxMB
		result.Sources["log.maxMB"] = SourceFile
	}
	return nil
}

// containsLogLevel reports whether level is one LogLevels names. The empty
// string is accepted: it is how the file says "let the build decide".
func containsLogLevel(level string) bool {
	if level == "" {
		return true
	}
	for _, known := range LogLevels {
		if known == level {
			return true
		}
	}
	return false
}
