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

// TestFetchImageSendsTheProvenanceReferer: an image host behind the same
// Referer-gated wall as its pages answers a Referer-less request with the
// wall, so the image loop arrives the way the page ladder does.
func TestFetchImageSendsTheProvenanceReferer(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 64)
	var referers []string
	gated := func(r *http.Request) (*http.Response, error) {
		referers = append(referers, r.Header.Get("Referer"))
		if r.Header.Get("Referer") == "" {
			return response(r, http.StatusForbidden, "text/html", "<html>Prove your humanity</html>"), nil
		}
		return response(r, http.StatusOK, "image/png", png), nil
	}
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: roundTripFunc(gated)},
		Chrome:   &http.Client{Transport: roundTripFunc(gated)},
	})
	got := h.FetchImage(context.Background(), "https://203.0.113.10/figure.png")
	if got.Error != "" || got.Method != rungDirect {
		t.Fatalf("Referer-gated image not fetched at the direct rung: method=%q error=%q referers=%q",
			got.Method, got.Error, referers)
	}
	if len(referers) == 0 || referers[0] != ProvenanceReferer {
		t.Fatalf("image request Referer = %q, want %q", referers, ProvenanceReferer)
	}
}

// TestFetchArchiveSendsTheProvenanceReferer pins the archive sibling of the
// image Referer: a host that answers 403 to a Referer-less request must be
// fetched at the direct rung, and the chrome-impersonation rung must send the
// same Referer when the direct rung fails.
func TestFetchArchiveSendsTheProvenanceReferer(t *testing.T) {
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
	gated := func(r *http.Request) (*http.Response, error) {
		referers = append(referers, r.Header.Get("Referer"))
		if r.Header.Get("Referer") == "" {
			return response(r, http.StatusForbidden, "text/html", "<html>Prove your humanity</html>"), nil
		}
		return response(r, http.StatusOK, "application/zip", zipBody.String()), nil
	}
	down := func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") }
	for _, tc := range []struct {
		name   string
		direct func(*http.Request) (*http.Response, error)
		method string
	}{
		{"direct rung", gated, rungDirect},
		{"impersonation rung", down, rungChromeImpersonation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			referers = nil
			h := mustNew(t, Options{
				CacheDir: t.TempDir(),
				Client:   &http.Client{Transport: roundTripFunc(tc.direct)},
				Chrome:   &http.Client{Transport: roundTripFunc(gated)},
			})
			path, got := h.fetchArchiveBytes(context.Background(), "https://203.0.113.10/bundle.zip", false)
			if got.Error != "" || path == "" || got.Method != tc.method {
				t.Fatalf("Referer-gated archive not fetched at the %s: method=%q error=%q referers=%q",
					tc.method, got.Method, got.Error, referers)
			}
			if len(referers) == 0 || referers[0] != ProvenanceReferer {
				t.Fatalf("archive request Referer = %q, want %q", referers, ProvenanceReferer)
			}
		})
	}
}
