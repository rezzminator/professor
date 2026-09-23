package harvest

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A rate limit's Retry-After travels to the caller: a 429 (or a 503 that
// carries the header) is named with the wait the server gave — "retry after
// N s" or "retry after <time>" — in the error text and in Result.RetryAfter,
// which stays empty when the server gave none. One note per public fetch
// (FetchPublic, Download) records what every response of that fetch said;
// decodedResponseBody, which every rung reads its answer through, writes it.

type retryAfterKey struct{}

type retryAfterNote struct {
	mu    sync.Mutex
	value string
}

func withRetryAfterNote(ctx context.Context) (context.Context, *retryAfterNote) {
	note := &retryAfterNote{}
	return context.WithValue(ctx, retryAfterKey{}, note), note
}

// noteRetryAfter records a rate-limit answer's Retry-After on the fetch's note.
func noteRetryAfter(resp *http.Response) {
	if resp == nil || resp.Request == nil ||
		(resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusServiceUnavailable) {
		return
	}
	note, ok := resp.Request.Context().Value(retryAfterKey{}).(*retryAfterNote)
	if !ok || note == nil { // nil: a reader or archive rung (withoutRetryAfterNote)
		return
	}
	if value := retryAfterValue(resp.Header.Get("Retry-After")); value != "" {
		note.mu.Lock()
		note.value = value
		note.mu.Unlock()
	}
}

// retryAfterValue renders a Retry-After header: delay-seconds as "N s", an
// HTTP date as its UTC time; "" for an absent or unparseable header.
func retryAfterValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		return strconv.FormatInt(seconds, 10) + " s"
	}
	if when, err := http.ParseTime(raw); err == nil {
		return when.UTC().Format(time.RFC1123)
	}
	return ""
}

// apply names the recorded wait on a rate-limited failure.
func (note *retryAfterNote) apply(result Result) Result {
	note.mu.Lock()
	value := note.value
	note.mu.Unlock()
	rateLimited := result.HTTPStatus == http.StatusTooManyRequests ||
		result.HTTPStatus == http.StatusServiceUnavailable ||
		result.ErrorKind == errorKindRateLimited
	if value == "" || result.Error == "" || !rateLimited {
		return result
	}
	result.RetryAfter = value
	result.Error = withRetryAfterText(result.Error, value)
	return result
}

// withRetryAfterText appends the server's wait to a failure text once.
func withRetryAfterText(text, value string) string {
	clause := "retry after " + value
	if value == "" || strings.Contains(text, clause) {
		return text
	}
	return strings.TrimSpace(text) + " The server said: " + clause + "."
}

// withoutRetryAfterNote is ctx for a rung that speaks to a reader or an
// archive instead of the target (Jina, defuddle.md, Wayback): their own rate
// limits are never recorded as the site's wait.
func withoutRetryAfterNote(ctx context.Context) context.Context {
	return context.WithValue(ctx, retryAfterKey{}, (*retryAfterNote)(nil))
}
