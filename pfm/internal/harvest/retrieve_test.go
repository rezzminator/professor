package harvest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

const retrievePNG = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00"

// TestRetrieveBinaryGuardNamesAFileUnderWantPage: a page read whose body is
// neither text nor a document the converter reads used to be converted as
// HTML and stored as binary characters marked successful. Watched FAILING
// before the guard (each body came back with Error "" and Kind html).
func TestRetrieveBinaryGuardNamesAFileUnderWantPage(t *testing.T) {
	for _, tc := range []struct {
		name, path, contentType, body, detected string
	}{
		{"wav", "/clip.wav", "audio/wav", "RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x44\xac\x00\x00", "audio/wave"},
		{"ogg", "/clip.ogg", "audio/ogg", "OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x01\x02\x03\x04", "application/ogg"},
		{"legacy doc", "/memo.doc", "application/msword", "\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1\x00\x00\x00\x00\x00\x00\x00\x00", "application/x-ole-storage"},
		{"unknown binary", "/blob", "application/octet-stream", "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0e\x0f", "application/octet-stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusOK, tc.contentType, tc.body), nil
			})
			h := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: site},
				Chrome:      &http.Client{Transport: site},
				Converter:   tagStripConverter(),
				BrowserRung: browserOff(),
			})
			got := h.Fetch(context.Background(), "https://203.0.113.10"+tc.path)
			if got.Error == "" || got.Kind != kindFile || got.ErrorKind != errorKindWrongKind {
				t.Fatalf("binary body under a page read: Error=%q Kind=%q ErrorKind=%q, want a named file result",
					got.Error, got.Kind, got.ErrorKind)
			}
			if !strings.Contains(got.Error, tc.detected) || !strings.Contains(got.Error, "download") {
				t.Fatalf("file result %q does not name the detected type %q and the download route",
					got.Error, tc.detected)
			}
		})
	}
}

// TestRetrieveBinaryGuardLeavesPagesAndArchivesAlone is the guard's regression
// side: a latin-1 HTML page still converts, and a zip keeps its own named
// archive refusal.
func TestRetrieveBinaryGuardLeavesPagesAndArchivesAlone(t *testing.T) {
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, ".zip") {
			return response(request, http.StatusOK, "application/zip", "PK\x03\x04\x14\x00\x00\x00"), nil
		}
		return response(
			request,
			http.StatusOK,
			"text/html; charset=iso-8859-1",
			"<html><head><title>Caf\xe9</title></head><body><p>Un caf\xe9 cr\xe8me, s'il vous pla\xeet.</p></body></html>",
		), nil
	})
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: site},
		Chrome:      &http.Client{Transport: site},
		Converter:   tagStripConverter(),
		BrowserRung: browserOff(),
	})
	if page := h.Fetch(context.Background(), "https://203.0.113.10/menu"); page.Error != "" {
		t.Fatalf("latin-1 HTML page refused by the binary guard: %q", page.Error)
	}
	archive := h.Fetch(context.Background(), "https://203.0.113.10/bundle.zip")
	if archive.Kind != kindArchive || !strings.Contains(archive.Error, "archive") {
		t.Fatalf("zip under a page read: Kind=%q Error=%q, want the archive refusal", archive.Kind, archive.Error)
	}
}

// countingBrowser is a Converter that counts browser starts.
type countingBrowser struct {
	fakeConverter
	mu     sync.Mutex
	starts int
}

func (b *countingBrowser) FetchBrowser(context.Context, string, bool) (string, int, string, error) {
	b.mu.Lock()
	b.starts++
	b.mu.Unlock()
	return "<html><body>rendered</body></html>", http.StatusOK, "", nil
}

// TestRetrieveInlineImagePolicyFallsToChromeAndNeverStartsABrowser: an inline
// image the direct rung is refused falls to Chrome impersonation, both with
// the page as Referer, and no rung ever starts a browser. Watched FAILING
// before PolicyInlineImage (the one-rung fetch left the image remote).
func TestRetrieveInlineImagePolicyFallsToChromeAndNeverStartsABrowser(t *testing.T) {
	const page = "https://203.0.113.10/article"
	var mu sync.Mutex
	var referers []string
	serve := func(ok bool) roundTripFunc {
		return func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			referers = append(referers, request.Header.Get(headerReferer))
			mu.Unlock()
			if !ok {
				return response(request, http.StatusForbidden, "text/html", "<html>blocked</html>"), nil
			}
			return response(request, http.StatusOK, "image/png", retrievePNG), nil
		}
	}
	browser := &countingBrowser{}
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: serve(false)},
		Chrome:      &http.Client{Transport: serve(true)},
		Converter:   browser,
		BrowserRung: browserOn(),
	})
	out, err := h.LocalizeImages(context.Background(), "![fig](https://203.0.113.10/fig.png)", page)
	if err != nil {
		t.Fatalf("LocalizeImages: %v", err)
	}
	if strings.Contains(out, "https://203.0.113.10/fig.png") {
		t.Fatalf("image refused by the direct rung was not fetched by Chrome impersonation: %q", out)
	}
	if browser.starts != 0 {
		t.Fatalf("the inline-image policy started %d browser(s)", browser.starts)
	}
	for _, referer := range referers {
		if referer != page {
			t.Fatalf("inline image rungs sent Referer %q, want the page %q (all: %v)", referer, page, referers)
		}
	}
}

// TestRetrieveFileTriesWaybackRaw: a file every live rung fails to serve is
// read from the Wayback raw copy (the id_ form). Watched FAILING before
// PolicyFile (Download stopped at Chrome impersonation).
func TestRetrieveFileTriesWaybackRaw(t *testing.T) {
	var archived string
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "web.archive.org" {
			archived = request.URL.String()
			return response(request, http.StatusOK, "image/png", retrievePNG), nil
		}
		return response(request, http.StatusNotFound, "text/html", "<html>gone</html>"), nil
	})
	wayback := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(
			request,
			`{"archived_snapshots":{"closest":{"available":true,"timestamp":"20260901000000"}}}`,
		), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: site},
		Chrome:   &http.Client{Transport: site},
		OA:       &http.Client{Transport: wayback},
	})
	got := h.Download(context.Background(), "https://203.0.113.10/figure.png")
	if got.Error != "" || got.Method != "wayback" {
		t.Fatalf("Download with a Wayback copy: Error=%q Method=%q rungs=%v", got.Error, got.Method, got.Rungs)
	}
	if !strings.Contains(archived, "20260901000000id_/https://203.0.113.10/figure.png") {
		t.Fatalf("the Wayback rung did not ask for the raw id_ copy: %q", archived)
	}
	if body, err := os.ReadFile(got.Path); err != nil || string(body) != retrievePNG {
		t.Fatalf("Wayback copy not stored in the binary cache: %q, %v", body, err)
	}
}

func sizedResponse(request *http.Request, body string, declared int64) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": {"application/octet-stream"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: declared,
		Request:       request,
	}
}

// TestRetrieveFileRungOrderFallsToChrome: the file policy climbs direct then
// Chrome impersonation; a transport failure on direct falls to the next rung
// and the kept rung is the one named.
func TestRetrieveFileRungOrderFallsToChrome(t *testing.T) {
	down := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })
	chrome := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return sizedResponse(request, "PK\x03\x04payload", -1), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: down},
		Chrome:   &http.Client{Transport: chrome},
	})
	got, err := h.Retrieve(context.Background(), "https://203.0.113.10/bundle.zip", WantFile, PolicyFile)
	if err != nil {
		t.Fatalf("Retrieve(file): %v", err)
	}
	if got.Result.Method != rungChromeImpersonation ||
		strings.Join(got.Rungs, ",") != rungDirect+","+rungChromeImpersonation {
		t.Fatalf("rungs %v method %q, want direct then chrome-impersonation", got.Rungs, got.Result.Method)
	}
	if body, err := os.ReadFile(got.Result.Path); err != nil || string(body) != "PK\x03\x04payload" {
		t.Fatalf("file not streamed into the binary cache: %q, %v", body, err)
	}
}

// TestRetrieveFileCapIsANamedFailure: a body over the download cap is a named
// failure, whether the server declared its size or streamed past the cap, and
// nothing is left in the cache.
func TestRetrieveFileCapIsANamedFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared int64
		want     string
	}{
		{"declared", 32, "declared 32 bytes"},
		{"streamed", -1, "ran past the 16-byte cap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return sizedResponse(request, strings.Repeat("\x00", 32), tc.declared), nil
			})
			cache := t.TempDir()
			h := mustNew(t, Options{
				CacheDir:         cache,
				Client:           &http.Client{Transport: site},
				Chrome:           &http.Client{Transport: site},
				MaxDownloadBytes: 16,
			})
			got, err := h.Retrieve(context.Background(), "https://203.0.113.10/big.bin", WantFile, PolicyFile)
			if !errors.Is(err, errDownloadTooLarge) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("over-cap body: err=%v, want errDownloadTooLarge naming %q", err, tc.want)
			}
			if got.Result.Path != "" || len(got.Rungs) != 1 {
				t.Fatalf("over-cap body kept a path %q or climbed on (rungs %v)", got.Result.Path, got.Rungs)
			}
			if failure := fileFailure(
				"https://203.0.113.10/big.bin",
				"file",
				got,
				err,
			); failure.ErrorKind != errorKindTooLarge {
				t.Fatalf("over-cap failure ErrorKind=%q, want %q", failure.ErrorKind, errorKindTooLarge)
			}
		})
	}
}

// TestRetrieveWantAndPolicyMustAgree: a page policy never downloads a file and
// a file policy never reads a page; each mismatch is a named error.
func TestRetrieveWantAndPolicyMustAgree(t *testing.T) {
	h := mustNew(t, Options{CacheDir: t.TempDir()})
	for _, tc := range []struct {
		want   Want
		policy Policy
	}{
		{WantFile, PolicyPage}, {WantPage, PolicyFile}, {WantPage, PolicyInlineImage}, {WantPage, Policy("bogus")},
	} {
		if _, err := h.Retrieve(context.Background(), "https://203.0.113.10/x", tc.want, tc.policy); err == nil {
			t.Fatalf("Retrieve(want %d, policy %q) accepted a mismatch", tc.want, tc.policy)
		}
	}
}

// TestRetrieveGatewayClimbsToTheBrowserForAPageNeverForAFile: the gateway
// policy runs direct → Chrome → headless browser on a wall; the same wall on a
// file stops before the browser, which renders HTML and has no bytes to give.
func TestRetrieveGatewayClimbsToTheBrowserForAPageNeverForAFile(t *testing.T) {
	wall := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", gatewayWallBody), nil
	})
	browser := &browserConverter{reply: func(bool) (string, int, error) {
		return "<html><body><h1>Record</h1></body></html>", http.StatusOK, nil
	}}
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: wall},
		Chrome:      &http.Client{Transport: wall},
		Converter:   browser,
		BrowserRung: enabled(),
	})
	page, err := h.Retrieve(context.Background(), "https://203.0.113.10/record", WantPage, PolicyGateway)
	if err != nil || !strings.Contains(string(page.Body), "Record") {
		t.Fatalf("gateway page: err=%v body=%q rungs=%v", err, page.Body, page.Rungs)
	}
	if strings.Join(page.Rungs, ",") != rungDirect+","+rungChromeImpersonation+",browser-headless" {
		t.Fatalf("gateway page rungs %v, want direct, chrome-impersonation, browser-headless", page.Rungs)
	}
	before := len(browser.headlessFlags())
	file, _ := h.Retrieve(context.Background(), "https://203.0.113.10/paper.pdf", WantFile, PolicyGateway)
	if len(browser.headlessFlags()) != before || !file.Challenge {
		t.Fatalf("gateway file started the browser (%d → %d) or lost the wall (challenge=%v)",
			before, len(browser.headlessFlags()), file.Challenge)
	}
}
