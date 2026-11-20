package config

import (
	"fmt"
	"strings"
	"time"
)

// NameSync is the window-name convergence schedule. Interval is rendered into
// BOTH schedulers at install time — the launchd job's StartInterval and the
// systemd timer's OnUnitInactiveSec — from this ONE value, so the two can
// never drift apart on a host that runs either.
type NameSync struct {
	Interval time.Duration
}

// DefaultNameSyncInterval is the poll the fleet shipped with: a claude
// /rename's backstop between picker runs.
const DefaultNameSyncInterval = 15 * time.Minute

// MinNameSyncInterval floors the poll. name-sync gathers the whole fleet and
// captures every claude pane; below a minute the poll costs more than the
// drift it converges.
const MinNameSyncInterval = time.Minute

// DefaultNameSync is the schedule when the file says nothing.
func DefaultNameSync() NameSync {
	return NameSync{Interval: DefaultNameSyncInterval}
}

type rawNameSync struct {
	Interval *string `json:"interval,omitempty"`
}

// parseNameSyncInterval refuses invalid nameSync.interval values rather than
// silently substituting a default for the scheduler installed from this value.
func parseNameSyncInterval(value, path string) (time.Duration, error) {
	interval, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf(
			"config %s: nameSync.interval must be a Go duration such as %q, got %q",
			path,
			DefaultNameSyncInterval.String(),
			value,
		)
	}
	if interval < MinNameSyncInterval {
		return 0, fmt.Errorf(
			"config %s: nameSync.interval must be at least %s, got %s",
			path,
			MinNameSyncInterval,
			interval,
		)
	}
	return interval, nil
}
