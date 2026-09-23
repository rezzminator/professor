package harvest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestRateLimitThroughTheLadderKeepsTheTargetsStatus: a target that answers
// 429 with Retry-After: 3600, readers that answer their own failures (Jina a
// 429 of its own with a different wait, defuddle.md a 502), a browser rung
// that fails and no Wayback copy — the public result reports the target's
// 429 and the target's wait, never a reader's status or a reader's wait.
func TestRateLimitThroughTheLadderKeepsTheTargetsStatus(t *testing.T) {
	const source = "https://slow.example/429"
	web := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "slow.example":
			answer := response(r, http.StatusTooManyRequests, "text/plain", "Too Many Requests")
			answer.Header.Set("Retry-After", "3600")
			return answer, nil
		case "defuddle.md":
			return response(r, http.StatusBadGateway, "text/plain", "upstream failed"), nil
		default:
			return response(r, http.StatusNotFound, "application/json", `{}`), nil
		}
	})
	reader := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		answer := response(r, http.StatusTooManyRequests, "text/plain", "reader rate limit")
		answer.Header.Set("Retry-After", "7")
		return answer, nil
	})
	missing := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusNotFound, "application/json", `{}`), nil
	})
	spy := &browserSpyConverter{err: errors.New("net::ERR_HTTP_RESPONSE_CODE_FAILURE")}
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: web},
		Chrome:      &http.Client{Transport: web},
		Jina:        &http.Client{Transport: reader},
		OA:          &http.Client{Transport: missing},
		Converter:   spy,
		BrowserRung: browserOn(),
	})
	got := h.FetchPublic(context.Background(), source, FetchOptions{Refresh: true})
	if spy.browserCalls == 0 {
		t.Fatalf("the browser rung never ran; rungs %v", got.Rungs)
	}
	if got.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("status = %d, want the target's 429 (error %q)", got.HTTPStatus, got.Error)
	}
	if got.RetryAfter != "3600 s" {
		t.Errorf("retry_after = %q, want the target's \"3600 s\" (never a reader's wait)", got.RetryAfter)
	}
	if !strings.Contains(got.Error, "retry after 3600 s") {
		t.Errorf("error %q does not name the target's wait", got.Error)
	}
}
