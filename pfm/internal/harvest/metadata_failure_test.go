package harvest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestMetadataOutageSurvivesMissingLibraryFallback(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	for _, mirror := range []string{"", "https://doi-mirror.test"} {
		t.Run("doi-mirror="+mirror, func(t *testing.T) {
			oa := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("metadata connection refused")
			})}
			var libraryLookups int
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "doi-mirror.test" {
					return response(r, http.StatusOK, "text/html", "<html>No document available</html>"), nil
				}
				if r.URL.Host != "md5-catalog.test" || r.URL.Path != "/json.php" {
					t.Errorf("unexpected library request: %s", r.URL)
				}
				libraryLookups++
				return response(r, http.StatusOK, "application/json", "[]"), nil
			})}
			h := mustNew(
				t,
				Options{
					CacheDir:      t.TempDir(),
					OA:            oa,
					Client:        client,
					Chrome:        &http.Client{Transport: client.Transport},
					DOIMirrorURL:  mirror,
					MD5CatalogURL: "https://md5-catalog.test",
				},
			)
			for _, mode := range []string{"exact-doi", "oa-pivot"} {
				t.Run(mode, func(t *testing.T) {
					libraryLookups = 0
					var got Result
					if mode == "exact-doi" {
						got = h.FetchPublic(context.Background(), providerFixtureDOI, FetchOptions{})
					} else {
						got = h.PublicResult(
							providerFixtureDOI,
							h.fetchOA(context.Background(), providerFixtureDOI, nil, FetchOptions{}),
							false,
						)
					}
					if libraryLookups != 1 {
						t.Fatalf("library lookups = %d; want the configured fallback attempted once", libraryLookups)
					}
					if got.Error == "" || got.ErrorKind != "connect" || got.Path != "" ||
						strings.Contains(strings.ToLower(got.Error), "not found") {
						t.Fatalf("metadata outage became an absence after a missing fallback: %#v", got)
					}
				})
			}
		})
	}
}
