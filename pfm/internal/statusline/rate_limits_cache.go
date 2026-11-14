package statusline

import (
	"errors"
	"fmt"
	"io"
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

// resetUnix reads one quota window's RFC3339 reset stamp for who's reading. An
// empty or unreadable stamp is 0, "no countdown": the reset only feeds the
// countdown and NewerReading's order, so the utilization it stamps is kept; an
// unreadable (not an empty) stamp is warned.
func resetUnix(warn io.Writer, who, key, stamp string) int64 {
	stamp = strings.TrimSpace(stamp)
	if stamp == "" {
		return 0
	}
	resetAt, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		fmt.Fprintf(
			warn,
			"statusline: %s quota: decode %s reset %q: %v (reading kept, no countdown)\n",
			who,
			key,
			stamp,
			err,
		)
		return 0
	}
	return resetAt.Unix()
}

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
	// An error beside found is a partial answer (usagehook): the snapshots that
	// read still answer, and the cause of each one skipped is told.
	if err != nil {
		fmt.Fprintf(runtime.Warn, "statusline: sibling quota snapshots %s: %v\n", runtime.RateLimitDir, err)
	}
	if !found {
		return
	}
	for _, named := range usage.NamedWindowsAt(now) {
		current, present := windows[named.Key]
		if !present || current.Failure || current.Absent || named.Window.Utilization == nil {
			continue
		}
		used := *named.Window.Utilization
		resetsAt := resetUnix(runtime.Warn, "sibling snapshot", named.Key, named.Window.ResetsAt)
		if usagehook.NewerReading(used, resetsAt, current.UsedPercentage, current.ResetsAt) {
			windows[named.Key] = rateWindow{UsedPercentage: used, ResetsAt: resetsAt, Cached: true}
		}
	}
}

// fillCachedRateWindows reads only the existing cache: rendering never requests
// provider data. It fills a missing window and replaces a reported one the
// cache holds a newer reading of (usagehook.NewerReading). The record's latest
// answer decides which windows exist: one it left out is hidden, steadily, on
// either side of a backoff and of the stale horizon. A record whose latest
// request failed marks ERR on the missing windows its last success carried
// (every missing one when no success is known) until a request succeeds; an
// unreadable cache marks every missing one. Cached provenance keeps these
// values out of fresh snapshots.
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
	confirmedAt := record.FetchedAt
	if confirmedAt == nil {
		confirmedAt = &modified
	}
	if confirmedAt.After(now) {
		// Clock skew on the confirmation stamp: the record is as fresh as it can be.
		fmt.Fprintf(
			runtime.Warn,
			"statusline: usage cache %s: confirmation timestamp %s is in the future (read as now)\n",
			path,
			confirmedAt.Format(time.RFC3339),
		)
		confirmedAt = &now
	}
	if record.Backoff != nil {
		fmt.Fprintf(runtime.Warn, "statusline: usage cache %s: last refresh failed, retry after %s: %q "+
			"(missing windows its last answer carried show ERR)\n",
			path, record.Backoff.RetryAfter.Format(time.RFC3339), record.Backoff.Message)
	}
	// A failed request keeps the last success's usage beside its backoff; with
	// no success stamped, the record knows no plan shape.
	knowsShape := record.Backoff == nil || record.FetchedAt != nil
	stale := now.Sub(*confirmedAt) > usagehook.StaleHorizon
	for _, descriptor := range usagehook.AllWindows() {
		key := descriptor.Key
		current, present := windows[key]
		if !missing[key] && (!present || current.Failure || current.Absent || record.Backoff != nil) {
			continue
		}
		var window usagehook.Window
		var windowErr error
		carried := false
		switch key {
		case fiveHourCacheKey:
			window, carried = record.FiveHour, record.FiveHour.Utilization != nil
		case sevenDayCacheKey:
			window, carried = record.SevenDay, record.SevenDay.Utilization != nil
		case fableCacheKey:
			_, anyFable, fableErr := record.FableWindowAt(time.Time{})
			carried = anyFable || fableErr != nil
			window, _, windowErr = record.FableWindowAt(now)
		}
		switch {
		case missing[key] && knowsShape && !carried:
			windows[key] = rateWindow{Absent: true}
			continue
		case missing[key] && record.Backoff != nil:
			windows[key] = rateWindow{Failure: true}
			continue
		case stale:
			continue
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
		cached.ResetsAt = resetUnix(runtime.Warn, "usage cache "+path, key, window.ResetsAt)
		if cached.ResetsAt != 0 && cached.ResetsAt <= now.Unix() {
			continue
		}
		if missing[key] ||
			usagehook.NewerReading(cached.UsedPercentage, cached.ResetsAt, current.UsedPercentage, current.ResetsAt) {
			windows[key] = cached
		}
	}
}
