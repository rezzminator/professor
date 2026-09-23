package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestSiteAPIRecordGapSurvivesTheReaderRung: when a site-API extractor's own
// record was not loaded and neither HTTP rung stores the page (Stack
// Exchange's wall; a GitHub page too thin to keep), the reader rung that
// stores it instead — Jina, or defuddle when Jina answers nothing — names the
// record's gap and why following stopped in its partial marker, once: what a
// reader renders is no more than the page shows, never the complete thread.
func TestSiteAPIRecordGapSurvivesTheReaderRung(t *testing.T) {
	read := "# Example thread\n\n" + strings.Repeat("a post the reader rendered ", 40)
	sites := []struct {
		name, source, gap string
		site              func() func(*http.Request) (*http.Response, error)
	}{
		{
			name:   "stackexchange, the question walled and its record throttled",
			source: seQuestionURL,
			gap:    "the question's API record was not loaded",
			site: func() func(*http.Request) (*http.Response, error) {
				site := seQuestionSite(t)
				site.status = map[string]int{seQuestionAPI: http.StatusBadRequest}
				site.bodies = map[string]string{seQuestionAPI: seThrottled}
				return site.roundTrip
			},
		},
		{
			name:   "github, the issue page thin and its record rate-limited",
			source: ghIssueURL,
			gap:    "the issue's API record was not loaded",
			site: func() func(*http.Request) (*http.Response, error) {
				site := ghIssueSite(t)
				site.status = map[string]int{ghIssueAPI: http.StatusForbidden}
				site.bodies = map[string]string{ghIssueAPI: ghRateLimited}
				return site.roundTrip
			},
		},
	}
	for _, tc := range sites {
		for _, reader := range []string{"jina", "defuddle-reader"} {
			t.Run(tc.name+" / "+reader, func(t *testing.T) {
				served := tc.site()
				readerAnswer := func(request *http.Request) *http.Response {
					return response(request, http.StatusOK, "text/plain; charset=utf-8", read)
				}
				missing := func(request *http.Request) *http.Response {
					return response(request, http.StatusNotFound, "application/json", `{}`)
				}
				h := mustNew(t, Options{
					CacheDir: t.TempDir(),
					Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
						if request.URL.Host == "defuddle.md" {
							if reader == "defuddle-reader" {
								return readerAnswer(request), nil
							}
							return missing(request), nil
						}
						return served(request)
					})},
					Chrome: &http.Client{Transport: roundTripFunc(served)},
					Jina: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
						if reader == "jina" {
							return readerAnswer(request), nil
						}
						return missing(request), nil
					})},
					OA: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
						return missing(request), nil
					})},
					Converter: &browserSpyConverter{
						convertFn: func(context.Context, string, string, []byte) (string, error) {
							return "a thin page", nil
						},
					},
					BrowserRung: browserOff(),
					Clock:       newPacingClock(),
				})
				result := h.FetchWithOptions(context.Background(), tc.source, FetchOptions{Refresh: true})
				if result.Error != "" || result.Method != reader {
					t.Fatalf("the reader's page was not stored: method=%q rungs=%v error=%q", result.Method,
						result.Rungs, result.Error)
				}
				if !strings.Contains(result.Content, "a post the reader rendered") {
					t.Fatalf("the artifact is not the reader's page:\n%.600s", result.Content)
				}
				for _, want := range []string{tc.gap, "rate limit exhausted"} {
					if !strings.Contains(result.Partial, want) {
						t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
					}
				}
				if strings.Count(result.Partial, tc.gap) != 1 {
					t.Fatalf("the partial marker names the gap more than once: %q", result.Partial)
				}
			})
		}
	}
}
