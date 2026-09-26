package harvest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestDownloadAllRungsFailingNamesTheOutage pins F14's media.go sibling:
// Download's static-rung loop used to fold a transport failure into the
// same fixed "image could not be downloaded" message a genuine non-image
// response gets. Watched FAILING before the fix (Error had no transport
// detail and ErrorKind was empty).
func TestDownloadAllRungsFailingNamesTheOutage(t *testing.T) {
	down := errors.New("connection refused")
	fail := func(*http.Request) (*http.Response, error) { return nil, down }
	// Two distinct client values (even with identical behavior): New()
	// treats an IDENTICAL Client/Chrome pointer as "unspecified Chrome" and
	// silently swaps in the real production uTLS transport.
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: roundTripFunc(fail)},
		Chrome:   &http.Client{Transport: roundTripFunc(fail)},
	})
	// A literal public-range address (TEST-NET-3, RFC 5737) skips DNS
	// resolution entirely at the SSRF pre-check, keeping this test hermetic.
	got := h.Download(context.Background(), "https://203.0.113.10/figure.png")
	if got.Error == "" {
		t.Fatal("Download with every rung failing returned no error")
	}
	if got.ErrorKind == "" || !strings.Contains(got.Error, down.Error()) {
		t.Fatalf("Download outage was not named (F14): Error=%q ErrorKind=%q", got.Error, got.ErrorKind)
	}
}

// TestDownloadSendsNoReferer: a direct image request (the caller handed the
// image URL itself, no harvested page in play) must carry no Referer at all.
// The fake host is hotlink-protected the REAL way — it 403s any foreign
// Referer (the old Google provenance one included) and only allows an empty
// one — so a lingering ProvenanceReferer send would read as a wall, not a
// pass.
func TestDownloadSendsNoReferer(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 64)
	var referers []string
	hotlinkProtected := func(r *http.Request) (*http.Response, error) {
		referer := r.Header.Get("Referer")
		referers = append(referers, referer)
		if referer != "" {
			return response(r, http.StatusForbidden, "text/html", "<html>hotlinking forbidden</html>"), nil
		}
		return response(r, http.StatusOK, "image/png", png), nil
	}
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: roundTripFunc(hotlinkProtected)},
		Chrome:   &http.Client{Transport: roundTripFunc(hotlinkProtected)},
	})
	got := h.Download(context.Background(), "https://203.0.113.10/figure.png")
	if got.Error != "" || got.Method != rungDirect {
		t.Fatalf("direct image request was walled: method=%q error=%q referers=%q",
			got.Method, got.Error, referers)
	}
	if len(referers) == 0 || referers[0] != "" {
		t.Fatalf("direct image request Referer = %q, want empty", referers)
	}
}
