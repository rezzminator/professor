package harvest

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

// TestConverterIsGivenTheAddressThePageWasFetchedFrom: a page's relative links
// resolve against where it was served from, not where it was asked for — the
// converter's context carries the final address after a redirect, and the
// requested one when there was none.
func TestConverterIsGivenTheAddressThePageWasFetchedFrom(t *testing.T) {
	t.Parallel()
	const requested = "https://docs.example.org/3/library/functions.html"
	for _, tc := range []struct {
		name   string
		hops   map[string]string
		status int
		want   string
	}{
		{"no redirect", nil, http.StatusOK, requested},
		{
			"a same-page move",
			map[string]string{requested: "https://docs.example.org/3/builtins/functions.html"},
			http.StatusMovedPermanently, "https://docs.example.org/3/builtins/functions.html",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu  sync.Mutex
				got []string
			)
			converter := &browserSpyConverter{
				convertFn: func(ctx context.Context, _, _ string, _ []byte) (string, error) {
					mu.Lock()
					defer mu.Unlock()
					got = append(got, BaseURLFrom(ctx))
					return landingArticle, nil
				},
			}
			origin := movingOrigin(tc.status, tc.hops)
			h := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: origin},
				Chrome:      &http.Client{Transport: origin},
				Converter:   converter,
				BrowserRung: browserOff(),
				Clock:       newPacingClock(),
			})
			result := h.FetchWithOptions(context.Background(), requested, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("the fetch failed: %q", result.Error)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(got) == 0 || got[0] != tc.want {
				t.Fatalf("the converter was given base URLs %q, want it first given %q", got, tc.want)
			}
		})
	}
}
