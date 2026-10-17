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

// fillCachedRateWindows reads only the existing cache: rendering never requests
// provider data. Cached provenance keeps these values out of fresh snapshots.
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
		if !missing[key] {
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
			windows[key] = rateWindow{Failure: true}
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
				windows[key] = rateWindow{Failure: true}
				continue
			}
			if !resetAt.After(now) {
				continue
			}
			cached.ResetsAt = resetAt.Unix()
		}
		windows[key] = cached
	}
}
