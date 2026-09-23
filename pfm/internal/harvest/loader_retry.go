package harvest

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// A loader request the site answers with HTTP 429 gets one wait and one
// retry: the wait is the server's Retry-After (retry_after.go), or
// loaderRetryDefault when it gave none, never shorter than the pace. One wait
// is at most loaderRetryWaitCap, and every wait of one fetch together at most
// loaderRetryBudget — a request the server asks to wait longer than either
// allows is not waited for, and the stop names the wait the server asked. A
// second 429 on the retried request ends the following, as the first once did.

const (
	// loaderRetryWaitCap is the longest single Retry-After a loader waits out.
	loaderRetryWaitCap = 60 * time.Second
	// loaderRetryBudget bounds every rate-limit wait of one fetch together.
	loaderRetryBudget = 120 * time.Second
	// loaderRetryDefault is the wait before the retry when the 429 carried no
	// usable Retry-After.
	loaderRetryDefault = 10 * time.Second
)

// loaderAttempt sends one loader request; a 429 is waited out and retried
// once, within the caps. rateStop is the reason the following ends when the
// answer it returns is still a 429; an error is the transport's, or the
// fetch's cancellation during the wait.
func (h *Harvester) loaderAttempt(
	ctx context.Context,
	loader pageLoader,
	request gatewayRequest,
	budget *loaderBudget,
) (response gatewayResponse, rateStop string, err error) {
	attemptCtx, note := withRetryAfterNote(ctx)
	response, err = gatewayAttempt(attemptCtx, request)
	if err != nil || response.status != http.StatusTooManyRequests {
		return response, "", err
	}
	raw := note.lastRetryAfter()
	asked := "gave no Retry-After"
	wait, given := retryAfterWait(raw, h.nowClock().Now())
	if given {
		asked = "asked to retry after " + retryAfterValue(raw)
	} else {
		wait = loaderRetryDefault
	}
	wait = max(wait, budget.pace)
	left := loaderRetryBudget - budget.rateLimitWaited
	prefix := fmt.Sprintf(
		"the site answered HTTP 429 (rate limited) after %d request(s) and %s",
		budget.requests,
		asked,
	)
	switch {
	case wait > loaderRetryWaitCap:
		return response, fmt.Sprintf("%s, past the %s one wait may take; not retried",
			prefix, waitSeconds(loaderRetryWaitCap)), nil
	case wait > left:
		return response, fmt.Sprintf("%s; a %s wait is past the %s left of the %s a fetch waits on rate limits; "+
			"not retried", prefix, waitSeconds(wait), waitSeconds(left), waitSeconds(loaderRetryBudget)), nil
	case budget.requests >= budget.limit:
		return response, fmt.Sprintf("%s; the cap of %d loader requests per page leaves no retry",
			prefix, budget.limit), nil
	}
	obs.Logger(ctx).Info("harvest: a loader request was rate limited; waiting before one retry",
		"kind", loader.label, "target", safeURL(loader.target), "wait", wait.String(), "retry_after", raw)
	budget.rateLimitWaited += wait
	if err := h.nowClock().Sleep(ctx, wait); err != nil {
		return response, "", err
	}
	budget.requests++
	attemptCtx, note = withRetryAfterNote(ctx)
	response, err = gatewayAttempt(attemptCtx, request)
	if err != nil || response.status != http.StatusTooManyRequests {
		return response, "", err
	}
	again := ""
	if value := retryAfterValue(note.lastRetryAfter()); value != "" {
		again = " (it asked to retry after " + value + ")"
	}
	return response, fmt.Sprintf("the site answered HTTP 429 (rate limited) after %d request(s), again after a %s "+
		"wait and one retry%s; not retried again", budget.requests, waitSeconds(wait), again), nil
}

// waitSeconds renders a wait as whole seconds: "60 s".
func waitSeconds(wait time.Duration) string {
	return strconv.FormatInt(int64(wait.Round(time.Second)/time.Second), 10) + " s"
}
