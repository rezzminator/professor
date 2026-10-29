package statusline

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

const (
	fiveHourCacheKey = "five_hour"
	sevenDayCacheKey = "seven_day"
	fableCacheKey    = "seven_day_fable"
)

// adoptSiblingReadings lets another seat of this account correct a reported
// window: an idle chat's payload stops at its last API reply while the account
// keeps spending. Only reported windows compete here; a missing one stays the
// usage cache's. Cached provenance keeps a borrowed reading out of this seat's
// own snapshot.
func adoptSiblingReadings(now time.Time, runtime Runtime, account int, windows map[string]rateWindow) {
	if runtime.Engine != pfmengine.Claude {
		return
	}
	usage, _, found, err := usagehook.ReadStatuslineSnapshot(
		runtime.RateLimitDir, account, runtime.ConfigDir, now, usagehook.StaleHorizon,
	)
	if err != nil {
		fmt.Fprintf(runtime.Warn, "statusline: sibling quota snapshots %s: %v\n", runtime.RateLimitDir, err)
		return
	}
	if !found {
		return
	}
	for _, named := range usage.NamedWindowsAt(now) {
		current, present := windows[named.Key]
		if !present || current.Failure || named.Window.Utilization == nil {
			continue
		}
		resetAt, err := time.Parse(time.RFC3339, named.Window.ResetsAt)
		if err != nil {
			fmt.Fprintf(runtime.Warn, "statusline: sibling quota snapshots: decode %s reset: %v\n", named.Key, err)
			continue
		}
		used := *named.Window.Utilization
		if usagehook.NewerReading(used, resetAt.Unix(), current.UsedPercentage, current.ResetsAt) {
			windows[named.Key] = rateWindow{UsedPercentage: used, ResetsAt: resetAt.Unix(), Cached: true}
		}
	}
}

// fillCachedRateWindows reads only the existing cache: rendering never requests
// provider data. It fills a missing window and replaces a reported one the
// cache holds a newer reading of (usagehook.NewerReading); a cache failure
// marks only the missing ones. Cached provenance keeps these values out of
// fresh snapshots.
func fillCachedRateWindows(
	now time.Time,
	runtime Runtime,
	account int,
	windows map[string]rateWindow,
	missing map[string]bool,
) {
	if runtime.Engine != pfmengine.Claude {
		return
	}
	path := usagehook.CachePath(usagehook.UsageCacheDir(runtime.CacheDir, runtime.UID), account)
	fail := func(err error) {
		fmt.Fprintf(runtime.Warn, "statusline: usage cache %s: %v (missing windows show ERR)\n", path, err)
		for key := range missing {
			windows[key] = rateWindow{Failure: true}
		}
	}
	record, modified, err := usagehook.ReadCacheRecordWithMtime(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fail(err)
		}
		return
	}
	if !record.MatchesConfigDir(runtime.ConfigDir) {
		return
	}
	if record.Backoff != nil && now.Before(record.Backoff.RetryAfter) {
		fail(
			fmt.Errorf(
				"refresh backoff until %s: %q",
				record.Backoff.RetryAfter.Format(time.RFC3339),
				record.Backoff.Message,
			),
		)
		return
	}
	confirmedAt := record.FetchedAt
	if confirmedAt == nil {
		confirmedAt = &modified
	}
	if confirmedAt.After(now) {
		fail(fmt.Errorf("cache confirmation timestamp %s is in the future", confirmedAt.Format(time.RFC3339)))
		return
	}
	if now.Sub(*confirmedAt) > usagehook.StaleHorizon {
		return
	}
	for _, descriptor := range usagehook.AllWindows() {
		key := descriptor.Key
		current, present := windows[key]
		if !missing[key] && (!present || current.Failure) {
			continue
		}
		var window usagehook.Window
		var windowErr error
		switch key {
		case fiveHourCacheKey:
			window = record.FiveHour
		case sevenDayCacheKey:
			window = record.SevenDay
		case fableCacheKey:
			window, _, windowErr = record.FableWindowAt(now)
		}
		if windowErr != nil {
			fmt.Fprintf(runtime.Warn, "statusline: usage cache %s: %v\n", path, windowErr)
			if missing[key] {
				windows[key] = rateWindow{Failure: true}
			}
			continue
		}
		if window.Utilization == nil {
			continue
		}
		cached := rateWindow{UsedPercentage: *window.Utilization, Cached: true}
		if stamp := strings.TrimSpace(window.ResetsAt); stamp != "" {
			resetAt, parseErr := time.Parse(time.RFC3339, stamp)
			if parseErr != nil {
				fmt.Fprintf(runtime.Warn, "statusline: usage cache %s: decode %s reset: %v\n", path, key, parseErr)
				if missing[key] {
					windows[key] = rateWindow{Failure: true}
				}
				continue
			}
			if !resetAt.After(now) {
				continue
			}
			cached.ResetsAt = resetAt.Unix()
		}
		if missing[key] ||
			usagehook.NewerReading(cached.UsedPercentage, cached.ResetsAt, current.UsedPercentage, current.ResetsAt) {
			windows[key] = cached
		}
	}
}
