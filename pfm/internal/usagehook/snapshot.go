package usagehook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// statuslineSnapshot is one provider-confirmed quota reading a running Claude
// seat handed its statusline on stdin (statusline.harvestRateLimits writes it).
type statuslineSnapshot struct {
	Account   int    `json:"acct"`
	ConfigDir string `json:"config_dir"`
	// Windows is every window the harness reported, keyed as on its stdin
	// (five_hour, seven_day, seven_day_opus, seven_day_fable, spend_limit, …).
	// A reader prefers it; the flat fields below are what a build before it
	// wrote, and what the writer still writes beside it for such a build.
	Windows          map[string]snapshotWindow `json:"windows"`
	FiveHourUsed     int64                     `json:"five_hour_used"`
	SevenDayUsed     int64                     `json:"seven_day_used"`
	FiveHourResetsAt int64                     `json:"five_hour_resets_at"`
	SevenDayResetsAt int64                     `json:"seven_day_resets_at"`
	// The Fable window is model-SCOPED: the harness reports it inside its
	// `limits` array, never as a flat top-level window, so it has to be
	// carried across this snapshot explicitly or it cannot reach a host whose
	// only limits source IS this snapshot. Absent in a snapshot written by an
	// older build, which reads back as a zero reset and is skipped.
	FableUsed     int64 `json:"fable_used"`
	FableResetsAt int64 `json:"fable_resets_at"`
	ConfirmedAt   int64 `json:"ts"`
}

// ClaudeRateLimitDir is the one filesystem rule for provider-confirmed Claude
// windows harvested from statusline input: the statusline writes there, and
// the usage door and the Limits tab read there. Production uses the host temp
// directory; a PFM_HOME jail keeps it inside that jail.
func ClaudeRateLimitDir(jailHome string) string {
	return filepath.Join(tempBase(jailHome), "cc-rate-limits")
}

// ReadStatuslineSnapshot returns the newest snapshot for account whose
// config_dir is configDir and whose confirmation is at most maxAge old, as
// Usage. Account number alone is not identity, so a snapshot written before
// config_dir was recorded, or belonging to a different config directory, is
// ignored. found=false with a nil error means nothing qualifying is on disk; an
// error means a snapshot could not be read or is malformed.
func ReadStatuslineSnapshot(
	directory string,
	account int,
	configDir string,
	now time.Time,
	maxAge time.Duration,
) (Usage, time.Time, bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return Usage{}, time.Time{}, false, nil
		}
		return Usage{}, time.Time{}, false, fmt.Errorf("read statusline quota directory: %w", err)
	}
	prefix := fmt.Sprintf("acct-%d.", account)
	var latest statuslineSnapshot
	var latestAt time.Time
	found := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return Usage{}, time.Time{}, false, fmt.Errorf("stat statusline quota %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return Usage{}, time.Time{}, false, fmt.Errorf("statusline quota %s is not a regular file", entry.Name())
		}
		body, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return Usage{}, time.Time{}, false, fmt.Errorf("read statusline quota %s: %w", entry.Name(), err)
		}
		var snapshot statuslineSnapshot
		if err := json.Unmarshal(body, &snapshot); err != nil {
			return Usage{}, time.Time{}, false, fmt.Errorf("decode statusline quota %s: %w", entry.Name(), err)
		}
		if snapshot.Account != account {
			return Usage{}, time.Time{}, false, fmt.Errorf(
				"statusline quota %s claims account %d, want %d", entry.Name(), snapshot.Account, account,
			)
		}
		if snapshot.ConfigDir == "" || !SameConfigDir(snapshot.ConfigDir, configDir) {
			continue
		}
		confirmedAt := time.Unix(snapshot.ConfirmedAt, 0)
		if snapshot.ConfirmedAt <= 0 || confirmedAt.After(now) {
			return Usage{}, time.Time{}, false, fmt.Errorf(
				"statusline quota %s has invalid confirmation time", entry.Name(),
			)
		}
		if now.Sub(confirmedAt) > maxAge {
			continue
		}
		if !found || confirmedAt.After(latestAt) {
			latest, latestAt, found = snapshot, confirmedAt, true
		}
	}
	if !found {
		return Usage{}, time.Time{}, false, nil
	}
	usage, err := latest.usage(now)
	if err != nil {
		return Usage{}, time.Time{}, false, err
	}
	if len(usage.NamedWindowsAt(now)) == 0 {
		return Usage{}, time.Time{}, false, nil
	}
	return usage, latestAt, true, nil
}

// snapshotWindow is one window as the harness reported it on stdin.
type snapshotWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
}

// usage keeps only windows whose reset is still ahead, read from the windows
// map when the snapshot carries one and from the flat fields an older build
// wrote otherwise. A window Usage has no field for (spend_limit) is not read.
func (snapshot statuslineSnapshot) usage(now time.Time) (Usage, error) {
	readings := snapshot.Windows
	if len(readings) == 0 {
		readings = map[string]snapshotWindow{
			fiveHourKey:      {UsedPercentage: float64(snapshot.FiveHourUsed), ResetsAt: snapshot.FiveHourResetsAt},
			sevenDayKey:      {UsedPercentage: float64(snapshot.SevenDayUsed), ResetsAt: snapshot.SevenDayResetsAt},
			sevenDayFableKey: {UsedPercentage: float64(snapshot.FableUsed), ResetsAt: snapshot.FableResetsAt},
		}
	}
	usage := Usage{}
	for key, reading := range readings {
		var target *Window
		switch key {
		case fiveHourKey:
			target = &usage.FiveHour
		case sevenDayKey:
			target = &usage.SevenDay
		case "seven_day_opus":
			target = &usage.SevenOpus
		case sevenDayFableKey:
		default:
			continue
		}
		if reading.ResetsAt <= now.Unix() {
			continue
		}
		if reading.UsedPercentage < 0 || reading.UsedPercentage > 100 {
			return Usage{}, fmt.Errorf(
				"statusline quota %s utilization %v is outside 0..100", key, reading.UsedPercentage,
			)
		}
		percent := reading.UsedPercentage
		resetsAt := time.Unix(reading.ResetsAt, 0).UTC().Format(time.RFC3339)
		if target != nil {
			*target = Window{Utilization: &percent, ResetsAt: resetsAt}
			continue
		}
		// Fable re-enters through the scoped `limits` array rather than a
		// flat field, because that array is the one shape fableWindow reads —
		// rebuilding the selector here would be a second opinion on which
		// scoped limit is the Fable one, and the two would drift.
		scoped := ScopedLimit{Kind: "weekly_scoped", Percent: &percent, ResetsAt: resetsAt, IsActive: true}
		scoped.Scope.Model.DisplayName = "Fable"
		usage.Limits = append(usage.Limits, scoped)
	}
	return usage, nil
}

// SameConfigDir compares two config directories by their physical location,
// falling back to the cleaned spelling when either cannot be resolved.
func SameConfigDir(left, right string) bool {
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(leftResolved) == filepath.Clean(rightResolved)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
