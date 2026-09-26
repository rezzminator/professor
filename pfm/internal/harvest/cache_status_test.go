package harvest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCacheHitCarriesTheDeliveringStatus: the status of the rung that
// delivered a page is stored with its cache entry, so a later cache hit
// reports the same status as the fresh read did.
func TestCacheHitCarriesTheDeliveringStatus(t *testing.T) {
	const source = "https://example.test/article"
	direct := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/html", strings.Repeat("<p>article body text</p>\n", 40)), nil
	})
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: direct},
		Chrome:    &http.Client{Transport: direct},
		Converter: &fakeConverter{},
	})
	fresh := h.Fetch(context.Background(), source)
	if fresh.Error != "" || fresh.CacheStatus != cacheStatusMiss || fresh.HTTPStatus != http.StatusOK {
		t.Fatalf("fresh read = error %q cache %q status %d, want a miss with status 200",
			fresh.Error, fresh.CacheStatus, fresh.HTTPStatus)
	}
	hit := h.Fetch(context.Background(), source)
	if hit.Error != "" || hit.CacheStatus != cacheStatusHit {
		t.Fatalf("second read = error %q cache %q, want a cache hit", hit.Error, hit.CacheStatus)
	}
	if hit.HTTPStatus != http.StatusOK {
		t.Fatalf("cache hit status = %d, want the delivering rung's 200", hit.HTTPStatus)
	}
}

// TestCacheEntryWithoutStatusReportsNone: an entry stored before the status
// was recorded carries no status on a hit, never a made-up 200.
func TestCacheEntryWithoutStatusReportsNone(t *testing.T) {
	const source = "https://example.test/older"
	h := mustNew(t, Options{CacheDir: t.TempDir(), Converter: &fakeConverter{}})
	path := h.cache.path(source, kindHTML)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	entry := "---\nurl: " + source + "\nfetched_at: 2099-01-01T00:00:00Z\nsource: harvester\nmethod: direct\n---\n\n" +
		strings.Repeat("stored article body text\n", 20)
	if err := os.WriteFile(path, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	hit := h.Fetch(context.Background(), source)
	if hit.Error != "" || hit.CacheStatus != cacheStatusHit {
		t.Fatalf("read = error %q cache %q, want a cache hit", hit.Error, hit.CacheStatus)
	}
	if hit.HTTPStatus != 0 {
		t.Fatalf("status-less entry reported status %d, want none", hit.HTTPStatus)
	}
}
