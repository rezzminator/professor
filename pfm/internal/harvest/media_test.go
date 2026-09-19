package harvest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestFetchImageAllRungsFailingNamesTheOutage pins F14's media.go sibling:
// FetchImage's static-rung loop used to fold a transport failure into the
// same fixed "image could not be downloaded" message a genuine non-image
// response gets. Watched FAILING before the fix (Error had no transport
// detail and ErrorKind was empty).
func TestFetchImageAllRungsFailingNamesTheOutage(t *testing.T) {
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
	got := h.FetchImage(context.Background(), "https://203.0.113.10/figure.png")
	if got.Error == "" {
		t.Fatal("FetchImage with every rung failing returned no error")
	}
	if got.ErrorKind == "" || !strings.Contains(got.Error, down.Error()) {
		t.Fatalf("FetchImage outage was not named (F14): Error=%q ErrorKind=%q", got.Error, got.ErrorKind)
	}
}

// TestFetchImageNonImageResponseStillReadsAsNotAnImage is the regression
// guard: a real (non-transport-failure) answer that just is not an image
// keeps its original, simpler message.
func TestFetchImageNonImageResponseStillReadsAsNotAnImage(t *testing.T) {
	answer := func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/html", "<html>not an image</html>"), nil
	}
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: roundTripFunc(answer)},
		Chrome:   &http.Client{Transport: roundTripFunc(answer)},
	})
	got := h.FetchImage(context.Background(), "https://203.0.113.10/figure.png")
	if got.Error != "image could not be downloaded" {
		t.Fatalf("FetchImage(non-image) Error = %q, want the unchanged message", got.Error)
	}
}
