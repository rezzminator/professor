package harvest

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A site that reports its request quota on every answer — Reddit's comment
// loaders send x-ratelimit-used, x-ratelimit-remaining ("199.0") and
// x-ratelimit-reset (seconds until the window resets), and answer a spent
// quota with a 429 that carries those and no Retry-After — is paced by it.
// While the quota is plentiful the pace is kept; once loaderQuotaReserve or
// fewer requests remain, the next ones are spread over what is left of the
// window, each wait at most loaderRetryWaitCap. A quota reported spent is
// waited out before the next request, and a 429 waits for the reset, both
// only within the one-wait cap and the fetch's rate-limit budget
// (loader_retry.go); past either, the following stops, naming when the quota
// resets. From the first request paced by a reported quota, the time the
// fetch spends — waits and answers together — counts toward
// loaderPacingBudget; the following stops before a wait and answer that would
// pass it, and the partial names what was loaded, the quota the headers
// reported and when the rest may be read (pacingLeft). A loader opts in (pageLoader.quotaHeaders): the same header names
// mean other things elsewhere (an epoch time on GitHub's API).

const (
	// loaderQuotaReserve is how many requests left in the site's quota start
	// the spread: above it the pace is kept.
	loaderQuotaReserve = 50
	// loaderQuotaSlack is added to a wait for the reset: the site rounds the
	// seconds it reports down.
	loaderQuotaSlack = time.Second
)

// quotaHeaders is the quota one answer reported: the requests left and the
// seconds until the window resets, as sent.
type quotaHeaders struct {
	used      float64
	usedKnown bool
	remaining float64
	reset     time.Duration
	reported  bool
}

// readQuotaHeaders reads x-ratelimit-remaining and x-ratelimit-reset, and
// x-ratelimit-used when sent; false when either of the first two is absent or
// unreadable.
func readQuotaHeaders(header http.Header) (quotaHeaders, bool) {
	remaining, err := strconv.ParseFloat(strings.TrimSpace(header.Get("X-Ratelimit-Remaining")), 64)
	if err != nil || remaining < 0 || math.IsNaN(remaining) || math.IsInf(remaining, 0) {
		return quotaHeaders{}, false
	}
	reset, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-Ratelimit-Reset")), 10, 64)
	if err != nil || reset < 0 || reset > int64(24*time.Hour/time.Second) {
		return quotaHeaders{}, false
	}
	quota := quotaHeaders{remaining: remaining, reset: time.Duration(reset) * time.Second, reported: true}
	used, err := strconv.ParseFloat(strings.TrimSpace(header.Get("X-Ratelimit-Used")), 64)
	if err == nil && used >= 0 && !math.IsNaN(used) && !math.IsInf(used, 0) {
		quota.used, quota.usedKnown = used, true
	}
	return quota, true
}

// loaderQuota is the site's quota as last reported during a fetch.
type loaderQuota struct {
	remaining float64
	used      float64
	usedKnown bool
	resetAt   time.Time
}

// noteQuota records the quota an answer reported at now.
func (budget *loaderBudget) noteQuota(quota quotaHeaders, now time.Time) {
	if quota.reported {
		budget.quota = &loaderQuota{
			remaining: quota.remaining, used: quota.used, usedKnown: quota.usedKnown,
			resetAt: now.Add(quota.reset),
		}
	}
}

// resetIn is the wait until the reported window resets, in whole seconds
// rounded up; 0 once it has.
func (quota *loaderQuota) resetIn(now time.Time) time.Duration {
	left := quota.resetAt.Sub(now)
	if left <= 0 {
		return 0
	}
	return (left + time.Second - 1).Truncate(time.Second)
}

// resetText names when the quota resets: "154 s (at Wed, 23 Sep 2026
// 20:30:00 UTC)".
func (quota *loaderQuota) resetText(now time.Time) string {
	return fmt.Sprintf("%s (at %s)", waitSeconds(quota.resetIn(now)), quota.resetAt.UTC().Format(time.RFC1123))
}

// quotaWait is the wait the site's reported quota asks before the next
// request: 0 while it is plentiful or once its window reset, the spread of
// the window over the requests left near its end, the reset when it is spent.
// A spent quota's wait is charged to the fetch's rate-limit budget; stop,
// when set, is why the next request is not sent.
func (budget *loaderBudget) quotaWait(now time.Time) (wait time.Duration, stop string) {
	quota := budget.quota
	if quota == nil {
		return 0, ""
	}
	resetIn := quota.resetIn(now)
	switch {
	case resetIn == 0 || quota.remaining > loaderQuotaReserve:
		return 0, ""
	case quota.remaining >= 1:
		spread := time.Duration(float64(resetIn) / math.Floor(quota.remaining))
		return min(spread.Round(time.Millisecond), loaderRetryWaitCap), ""
	}
	wait = resetIn + loaderQuotaSlack
	left := loaderRetryBudget - budget.rateLimitWaited
	prefix := fmt.Sprintf("the site reported its request quota spent after %d request(s), resetting in %s",
		budget.requests, quota.resetText(now))
	switch {
	case wait > loaderRetryWaitCap:
		return 0, fmt.Sprintf("%s, past the %s one wait may take; not requested", prefix,
			waitSeconds(loaderRetryWaitCap))
	case wait > left:
		return 0, fmt.Sprintf("%s; a %s wait is past the %s left of the %s a fetch waits on rate limits; "+
			"not requested", prefix, waitSeconds(wait), waitSeconds(left), waitSeconds(loaderRetryBudget))
	}
	budget.rateLimitWaited += wait
	return wait, ""
}

// siteQuota is the request quota a site states: requests per window.
type siteQuota struct {
	site     string
	requests int
	window   time.Duration
}

// commentCount is a thread's comments: loaded of stated.
type commentCount struct {
	loaded, stated int
}

// pacingLeft names, once the pacing budget ended the following, what was
// loaded, the quota the site's headers reported (used plus remaining a
// window), how long the rest would take (at the comments per request this
// fetch loaded) and when the quota resets. Without a reported used count it
// falls back to the site's documented rate, named as such. "" when the
// budget did not stop it or the site documents no quota.
func (budget *loaderBudget) pacingLeft(count commentCount, now time.Time) string {
	if budget == nil || !budget.pacingStop || budget.quotaRate == nil {
		return ""
	}
	rate := budget.quotaRate
	rest := max(count.stated-count.loaded, 0)
	restRequests := rest
	if count.loaded > 0 && budget.requests > 0 {
		restRequests = int(math.Ceil(float64(rest) * float64(budget.requests) / float64(count.loaded)))
	}
	reset := "the quota resets (the site reported no x-ratelimit-reset)"
	resetIn := time.Duration(0)
	if budget.quota != nil {
		resetIn = budget.quota.resetIn(now)
		reset = now.Add(resetIn).UTC().Format("15:04:05 UTC")
	}
	quota := budget.quota
	var stated string
	var restTime time.Duration
	if quota != nil && quota.usedKnown && quota.used+quota.remaining >= 1 {
		// The window's length is not in the headers: past the current window
		// each further one is taken to last the documented window.
		perWindow := int(quota.used + quota.remaining)
		if beyond := restRequests - int(math.Floor(quota.remaining)); beyond > 0 {
			windows := (beyond + perWindow - 1) / perWindow
			restTime = resetIn + time.Duration(windows-1)*rate.window
		}
		stated = fmt.Sprintf("%s reported a quota of %d requests a window (%d used, %d left, resetting at %s) and",
			rate.site, perWindow, int(quota.used), int(quota.remaining), reset)
	} else {
		restTime = time.Duration(restRequests) * rate.window / time.Duration(rate.requests)
		stated = fmt.Sprintf("the site sent no quota header, and %s's documented rate is %d requests per %d minutes:",
			rate.site, rate.requests, int(rate.window.Minutes()))
	}
	minutes := max(int(math.Ceil(restTime.Minutes())), 1)
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("%d of %d comments loaded; %s the rest would take about %d more %s — read it again with "+
		"refresh after %s to continue", count.loaded, count.stated, stated, minutes, unit, reset)
}
