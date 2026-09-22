package harvest

import (
	"archive/zip"
	"bytes"
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

// TestFetchImageSendsNoReferer: a direct image request (the caller handed the
// image URL itself, no harvested page in play) must carry no Referer at all.
// The fake host is hotlink-protected the REAL way — it 403s any foreign
// Referer (the old Google provenance one included) and only allows an empty
// one — so a lingering ProvenanceReferer send would read as a wall, not a
// pass.
func TestFetchImageSendsNoReferer(t *testing.T) {
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
	got := h.FetchImage(context.Background(), "https://203.0.113.10/figure.png")
	if got.Error != "" || got.Method != rungDirect {
		t.Fatalf("direct image request was walled: method=%q error=%q referers=%q",
			got.Method, got.Error, referers)
	}
	if len(referers) == 0 || referers[0] != "" {
		t.Fatalf("direct image request Referer = %q, want empty", referers)
	}
}

// TestFetchArchiveSendsNoReferer is the archive sibling of
// TestFetchImageSendsNoReferer: a directly requested archive URL carries no
// Referer, on both the direct and the chrome-impersonation rung, against a
// host that 403s any foreign Referer and only allows an empty one.
func TestFetchArchiveSendsNoReferer(t *testing.T) {
	var zipBody bytes.Buffer
	zw := zip.NewWriter(&zipBody)
	member, err := zw.Create("paper.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := member.Write([]byte("archived paper")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var referers []string
	hotlinkProtected := func(r *http.Request) (*http.Response, error) {
		referer := r.Header.Get("Referer")
		referers = append(referers, referer)
		if referer != "" {
			return response(r, http.StatusForbidden, "text/html", "<html>hotlinking forbidden</html>"), nil
		}
		return response(r, http.StatusOK, "application/zip", zipBody.String()), nil
	}
	down := func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }
	for _, tc := range []struct {
		name   string
		direct func(*http.Request) (*http.Response, error)
		method string
	}{
		{"direct rung", hotlinkProtected, rungDirect},
		{"impersonation rung", down, rungChromeImpersonation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			referers = nil
			h := mustNew(t, Options{
				CacheDir: t.TempDir(),
				Client:   &http.Client{Transport: roundTripFunc(tc.direct)},
				Chrome:   &http.Client{Transport: roundTripFunc(hotlinkProtected)},
			})
			path, got := h.fetchArchiveBytes(context.Background(), "https://203.0.113.10/bundle.zip", false)
			if got.Error != "" || path == "" || got.Method != tc.method {
				t.Fatalf("direct archive request was walled at the %s: method=%q error=%q referers=%q",
					tc.method, got.Method, got.Error, referers)
			}
			if len(referers) == 0 || referers[0] != "" {
				t.Fatalf("direct archive request Referer = %q, want empty", referers)
			}
		})
	}
}
