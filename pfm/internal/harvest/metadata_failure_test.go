package harvest

import (
	"context"
	"errors"
	"fmt"
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

// TestProviderRefusalIsNeverTheAccessPolicyRefusal: errorKindBlocked is the
// harvester's own refusal of a private or internal host, which the public
// answer renders as "refused by access policy". A provider that answers 401 or
// 403 refused the harvester (forbidden), and a transport error that merely
// spells "internal" (an HTTP/2 INTERNAL_ERROR) is a failed connection. The
// harvester's own refusals, the browser rung's SSRF guard among them, stay
// blocked.
func TestProviderRefusalIsNeverTheAccessPolicyRefusal(t *testing.T) {
	for _, testCase := range []struct {
		name string
		kind string
		want string
	}{
		{"catalogue answered 403", doiMetadataFailureKind(errors.New("library.oapen.org returned HTTP 403 (forbidden)")), errorKindForbidden},
		{"catalogue answered 401", doiMetadataFailureKind(errors.New("api.example.test returned HTTP 401 (unauthorized)")), errorKindForbidden},
		{"http2 internal error", errorKind(errors.New("stream error: stream ID 1; INTERNAL_ERROR; received from peer")), errorKindConnect},
		{"not allowed in a server's words", errorKind(errors.New("read: your address is not allowed to access this API")), errorKindConnect},
		{"own private-host refusal", errorKind(errors.New("refusing private/internal host 10.0.0.1")), errorKindBlocked},
		{"own userinfo refusal", errorKind(errors.New("URL userinfo is not allowed")), errorKindBlocked},
		{"own browser policy refusal", errorKind(fmt.Errorf("browser download: %w", ErrBrowserPolicyDenied)), errorKindBlocked},
	} {
		if testCase.kind != testCase.want {
			t.Errorf("%s: kind = %q, want %q", testCase.name, testCase.kind, testCase.want)
		}
	}
}

// TestISBNWhoseCataloguesRefuseIsAnHonestBookFailure is the ISBN read refused
// "by access policy": OAPEN and DOAB answer 403 to the harvester's address and
// the other book catalogues list no open copy. The read names that, with the
// status, and never claims the harvester's own access policy refused it.
func TestISBNWhoseCataloguesRefuseIsAnHonestBookFailure(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "library.oapen.org", "directory.doabooks.org":
			return response(r, http.StatusForbidden, "text/html; charset=utf-8",
				"You address is not allowed to access this API."), nil
		case "catalog.hathitrust.org":
			return jsonResponse(r, `{"records":{},"items":[]}`), nil
		}
		return response(r, http.StatusNotFound, "application/json", `{"error":"notfound"}`), nil
	})
	client := &http.Client{Transport: transport}
	h := mustNew(
		t,
		Options{CacheDir: t.TempDir(), Client: client, OA: fixtureTwin(client), Chrome: fixtureTwin(client)},
	)
	got := h.FetchPublic(context.Background(), "9780262033848", FetchOptions{Field: "publications"})
	want := "No open copy of this book was found: the book catalogues that answered list none, and the others " +
		"refused the harvester (HTTP 403 Forbidden: a bot block or an access rule, which the harvester cannot tell apart; " +
		"it never signs in). Search for the book with harvester_search_literature, or read a public copy with harvester_read (urls)."
	if got.ErrorKind != errorKindForbidden || got.Error != want {
		t.Fatalf(
			"ISBN read = kind %q error %q\nwant kind %q error %q",
			got.ErrorKind,
			got.Error,
			errorKindForbidden,
			want,
		)
	}
	// The read tool and `pfm harvest` render the public result's message again
	// (harvestmcp itemFailure): the book text must survive that second pass.
	if again := PublicFailureMessage(got); again != want {
		t.Fatalf("ISBN read rendered again = %q\nwant %q", again, want)
	}
}
