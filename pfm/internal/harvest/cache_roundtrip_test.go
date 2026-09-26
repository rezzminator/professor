package harvest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The current source passes this round trip; the release-day write-only result
// was therefore a stale binary finding. Keep the local-server pin so a future
// build cannot regress the read path unnoticed.
func TestCacheRoundTripDoesNotTouchDiskOrNetwork(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits++
		writer.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(writer, "<html><body>"+strings.Repeat("cached article ", 80)+"</body></html>")
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	harvester := mustNew(t, Options{
		CacheDir:  cacheDir,
		Client:    &http.Client{Transport: localServerTransport{base: http.DefaultTransport, target: server.URL}},
		Converter: &fakeConverter{},
	})
	first := harvester.Fetch(context.Background(), "http://example.test/article")
	if first.Error != "" || first.CacheStatus != "miss" || hits != 1 {
		t.Fatalf("first fetch=%#v hits=%d", first, hits)
	}
	files, err := filepath.Glob(filepath.Join(cacheDir, "html", "*.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("cache files=%v err=%v", files, err)
	}
	before, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}

	second := harvester.Fetch(context.Background(), "http://example.test/article")
	after, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if second.Error != "" || second.CacheStatus != "hit" || hits != 1 {
		t.Fatalf("second fetch=%#v hits=%d", second, hits)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("cache mtime changed from %s to %s", before.ModTime(), after.ModTime())
	}
}

func TestVolatileCacheTTLBackdatedStampAndZeroOverride(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits++
		writer.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(writer, "<html><body>"+strings.Repeat("ttl article ", 80)+"</body></html>")
	}))
	defer server.Close()

	cacheDir := t.TempDir()
	client := &http.Client{Transport: localServerTransport{base: http.DefaultTransport, target: server.URL}}
	harvester := mustNew(t, Options{CacheDir: cacheDir, Client: client, Converter: &fakeConverter{}})
	source := "http://example.test/ttl"
	if result := harvester.Fetch(context.Background(), source); result.Error != "" {
		t.Fatalf("seed fetch=%#v", result)
	}
	path := cacheHTMLPath(t, cacheDir)
	setCacheStamp(t, path, time.Now().Add(-23*time.Hour))
	if result := harvester.Fetch(
		context.Background(),
		source,
	); result.Error != "" || result.CacheStatus != "hit" ||
		hits != 1 {
		t.Fatalf("young cache=%#v hits=%d", result, hits)
	}
	setCacheStamp(t, path, time.Now().Add(-25*time.Hour))
	if result := harvester.Fetch(
		context.Background(),
		source,
	); result.Error != "" || result.CacheStatus != "miss" ||
		hits != 2 {
		t.Fatalf("old cache=%#v hits=%d", result, hits)
	}

	noExpiry := mustNew(t, Options{CacheDir: t.TempDir(), CacheTTL: -1, Client: client, Converter: &fakeConverter{}})
	zeroSource := "http://example.test/never-stale"
	if result := noExpiry.Fetch(context.Background(), zeroSource); result.Error != "" {
		t.Fatalf("zero seed fetch=%#v", result)
	}
	zeroPath := cacheHTMLPath(t, noExpiry.options.CacheDir)
	setCacheStamp(t, zeroPath, time.Now().Add(-72*time.Hour))
	before := hits
	if result := noExpiry.Fetch(
		context.Background(),
		zeroSource,
	); result.Error != "" || result.CacheStatus != "hit" ||
		hits != before {
		t.Fatalf("zero ttl cache=%#v hits=%d before=%d", result, hits, before)
	}
}

type localServerTransport struct {
	base   http.RoundTripper
	target string
}

func (transport localServerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	target, err := url.Parse(transport.target)
	if err != nil {
		return nil, err
	}
	clone := request.Clone(request.Context())
	clone.URL.Scheme = target.Scheme
	clone.URL.Host = target.Host
	return transport.base.RoundTrip(clone)
}

func cacheHTMLPath(t *testing.T, root string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "html", "*.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("cache files=%v err=%v", files, err)
	}
	return files[0]
}

func setCacheStamp(t *testing.T, path string, stamp time.Time) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, "fetched_at: ") {
			lines[index] = "fetched_at: " + stamp.UTC().Format(time.RFC3339)
			break
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}
