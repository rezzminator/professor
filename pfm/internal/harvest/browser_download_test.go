package harvest

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const challengePage = "<html><head><title>Just a moment...</title></head><body>Checking your browser</body></html>"

// downloadingBrowser is a BrowserDownloader: it records the source of every
// call and writes body to the path Go named, ignoring the cap it was
// handed so the Go side's own cap is what a test sees hold.
type downloadingBrowser struct {
	fakeConverter
	mu          sync.Mutex
	calls       []string
	body        string
	contentType string
	err         error // returned by every call
}

func (b *downloadingBrowser) DownloadBrowser(
	_ context.Context,
	source, dest string,
	_ int64,
) (BrowserFile, error) {
	b.mu.Lock()
	b.calls = append(b.calls, source)
	b.mu.Unlock()
	if b.err != nil {
		return BrowserFile{}, b.err
	}
	if err := os.WriteFile(dest, []byte(b.body), 0o600); err != nil {
		return BrowserFile{}, err
	}
	return BrowserFile{
		ContentType: b.contentType,
		Bytes:       int64(len(b.body)),
		FinalURL:    source,
		Status:      http.StatusOK,
	}, nil
}

// walledHarvester is a harvester whose direct and Chrome rungs meet a 403
// challenge page and whose Wayback lookup finds no copy, with browser as its
// converter and the browser rung on.
func walledHarvester(t *testing.T, browser Converter, maxBytes int64) (*Harvester, string) {
	t.Helper()
	wall := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", challengePage), nil
	})
	noCopy := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, `{"archived_snapshots":{}}`), nil
	})
	cache := t.TempDir()
	return mustNew(t, Options{
		CacheDir:         cache,
		Client:           &http.Client{Transport: wall},
		Chrome:           &http.Client{Transport: wall},
		OA:               &http.Client{Transport: noCopy},
		Converter:        browser,
		BrowserRung:      browserOn(),
		MaxDownloadBytes: maxBytes,
	}), cache
}

// leftovers lists the browser rung's scratch files still in the cache.
func leftovers(t *testing.T, cache string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(cache, ".browser-download-*"))
	if err != nil {
		t.Fatalf("glob the cache: %v", err)
	}
	return matches
}

// TestRetrieveFileBrowserDownloadIsTheLastRung: a file direct, Chrome and
// Wayback cannot serve is downloaded by the browser, headless, and lands in
// the binary cache under the browser-download method. Watched FAILING before
// the rung (PolicyFile stopped at Wayback: "no rung served the file").
func TestRetrieveFileBrowserDownloadIsTheLastRung(t *testing.T) {
	browser := &downloadingBrowser{body: "%PDF-1.7\nbrowser bytes", contentType: "application/pdf"}
	h, cache := walledHarvester(t, browser, 0)
	got := h.Download(context.Background(), "https://203.0.113.10/paper.pdf")
	if got.Error != "" || got.Method != rungBrowserDownload {
		t.Fatalf("walled file: Error=%q Method=%q rungs=%v", got.Error, got.Method, got.Rungs)
	}
	if want := rungDirect + "," + rungChromeImpersonation + "," + rungBrowserDownload; strings.Join(
		got.Rungs,
		",",
	) != want {
		t.Fatalf("rungs %v, want %s", got.Rungs, want)
	}
	if len(browser.calls) != 1 {
		t.Fatalf("browser calls %v, want one", browser.calls)
	}
	if !strings.HasPrefix(got.Path, cache+string(filepath.Separator)) || got.Kind != kindPDF ||
		got.Bytes != int64(len(browser.body)) {
		t.Fatalf(
			"browser file not in the binary cache as a pdf: path=%q kind=%q bytes=%d",
			got.Path,
			got.Kind,
			got.Bytes,
		)
	}
	if body, err := os.ReadFile(got.Path); err != nil || string(body) != browser.body {
		t.Fatalf("browser file bytes %q, %v; want %q", body, err, browser.body)
	}
	if left := leftovers(t, cache); len(left) != 0 {
		t.Fatalf("the browser rung left scratch in the cache: %v", left)
	}
}

// TestRetrieveFileBrowserFailureIsNamed: a browser that does not produce the
// file is a named failure, never an empty success, from its one headless
// call. Watched FAILING before the rung (the browser was never called).
func TestRetrieveFileBrowserFailureIsNamed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCalls int
		want      string
	}{
		{
			"challenge", &BrowserDownloadError{Reason: BrowserDownloadNoDownload, Status: 403, Head: challengePage}, 1,
			"challenge the browser did not pass",
		},
		{
			"page", &BrowserDownloadError{Reason: BrowserDownloadNoDownload, Status: 200, Head: "<html><body>Welcome</body></html>"}, 1,
			"no download started",
		},
		{"timeout", &BrowserDownloadError{Reason: BrowserDownloadTimeout, Detail: "45000ms"}, 1, "timed out"},
		{"outage", errors.New("patchright not installed"), 1, "could not run: patchright not installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			browser := &downloadingBrowser{err: tc.err}
			h, cache := walledHarvester(t, browser, 0)
			got := h.Download(context.Background(), "https://203.0.113.10/paper.pdf")
			if got.Error == "" || got.Path != "" {
				t.Fatalf("failed browser download: Error=%q Path=%q, want a named failure", got.Error, got.Path)
			}
			if !strings.Contains(got.Error, rungBrowserDownload) || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("failure %q does not name the browser rung and %q", got.Error, tc.want)
			}
			if len(browser.calls) != tc.wantCalls {
				t.Fatalf("browser calls %v, want %d", browser.calls, tc.wantCalls)
			}
			if left := leftovers(t, cache); len(left) != 0 {
				t.Fatalf("the browser rung left scratch in the cache: %v", left)
			}
		})
	}
}

// TestRetrieveFileBrowserCapHolds: the download cap holds on the browser rung,
// whether the worker names the overrun or writes past the cap. Watched FAILING
// before the rung.
func TestRetrieveFileBrowserCapHolds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		browser *downloadingBrowser
	}{
		{"worker named", &downloadingBrowser{err: &BrowserDownloadError{Reason: BrowserDownloadTooLarge}}},
		{"written past", &downloadingBrowser{body: strings.Repeat("\x00", 32)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cache := walledHarvester(t, tc.browser, 16)
			got, err := h.Retrieve(context.Background(), "https://203.0.113.10/big.bin", WantFile, PolicyFile)
			if !errors.Is(err, errDownloadTooLarge) || !strings.Contains(err.Error(), "16-byte cap") {
				t.Fatalf("over-cap browser download: err=%v, want errDownloadTooLarge naming the 16-byte cap", err)
			}
			if got.Result.Path != "" {
				t.Fatalf("over-cap browser download kept %q", got.Result.Path)
			}
			kept, globErr := filepath.Glob(filepath.Join(cache, "*", "*.bin"))
			if globErr != nil || len(kept) != 0 || len(leftovers(t, cache)) != 0 {
				t.Fatalf("over-cap browser download left %v in the cache (%v)", kept, globErr)
			}
		})
	}
}
