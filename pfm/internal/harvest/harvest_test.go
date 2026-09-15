package harvest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingTransport struct {
	mu      sync.Mutex
	seen    []string
	respond func(*http.Request) (*http.Response, error)
}

func (t *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.seen = append(t.seen, r.URL.String())
	t.mu.Unlock()
	return t.respond(r)
}

func response(req *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

type fakeConverter struct{ calls int }

func (c *fakeConverter) Convert(_ context.Context, kind, source string, body []byte) (string, error) {
	c.calls++
	if kind == "html" {
		return string(body), nil
	}
	return "converted:" + source, nil
}

func TestFetchLadderDirectChromeJinaAndPrivateSkip(t *testing.T) {
	cacheDir := t.TempDir()
	converter := &fakeConverter{}
	direct := &recordingTransport{respond: func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", "<html><body>checking your browser</body></html>"), nil
	}}
	chrome := &recordingTransport{respond: func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", "<html><body>checking your browser</body></html>"), nil
	}}
	jina := &recordingTransport{respond: func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.URL.String(), "https://r.jina.ai/") {
			return nil, errors.New("unexpected jina URL")
		}
		return response(r, http.StatusOK, "text/plain", strings.Repeat("rich article text ", 60)), nil
	}}
	h := mustNew(t, Options{
		CacheDir:  cacheDir,
		Client:    &http.Client{Transport: direct},
		Chrome:    &http.Client{Transport: chrome},
		Jina:      &http.Client{Transport: jina},
		Converter: converter,
	})
	got := h.Fetch(context.Background(), "https://example.test/article")
	if got.Error != "" {
		t.Fatalf("Fetch() error = %q", got.Error)
	}
	if got.Method != "jina" || len(got.Rungs) != 3 {
		t.Fatalf("ladder result = method %q rungs %#v, want jina after three rungs", got.Method, got.Rungs)
	}
	if converter.calls != 2 {
		t.Fatalf("converter calls = %d, want direct and Chrome conversion only", converter.calls)
	}

	private := &recordingTransport{respond: func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/plain", strings.Repeat("private page ", 60)), nil
	}}
	h = mustNew(
		t,
		Options{
			CacheDir:  t.TempDir(),
			Client:    &http.Client{Transport: private},
			Chrome:    &http.Client{Transport: private},
			Jina:      &http.Client{Transport: private},
			Converter: converter,
		},
	)
	got = h.Fetch(context.Background(), "http://127.0.0.1/private")
	if got.Error == "" || !strings.Contains(strings.ToLower(got.Error), "private") {
		t.Fatalf("private fetch error = %q, want explicit local/private refusal", got.Error)
	}
	private.mu.Lock()
	seen := append([]string(nil), private.seen...)
	private.mu.Unlock()
	if len(seen) != 0 {
		t.Fatalf("private request reached transport: %#v", seen)
	}
}

func TestCacheTypePartitionTTLAndRefresh(t *testing.T) {
	var count int
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		count++
		return response(r, http.StatusOK, "text/html", strings.Repeat("body ", 150)), nil
	})
	cacheDir := t.TempDir()
	h := mustNew(
		t,
		Options{
			CacheDir:  cacheDir,
			Client:    &http.Client{Transport: tr},
			Chrome:    &http.Client{Transport: tr},
			CacheTTL:  time.Hour,
			Converter: &fakeConverter{},
		},
	)
	first := h.Fetch(context.Background(), "https://example.test/a")
	if first.Error != "" || first.CacheStatus != "miss" {
		t.Fatalf("first fetch = %#v", first)
	}
	second := h.Fetch(context.Background(), "https://example.test/a")
	if second.Error != "" || second.CacheStatus != "hit" || count != 1 {
		t.Fatalf("cached fetch = %#v, count=%d", second, count)
	}
	third := h.FetchWithOptions(context.Background(), "https://example.test/a", FetchOptions{Refresh: true})
	if third.Error != "" || third.CacheStatus != "refresh" || count != 2 {
		t.Fatalf("refresh fetch = %#v, count=%d", third, count)
	}
	files, err := filepath.Glob(filepath.Join(cacheDir, "html", "*.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("html cache files = %#v err=%v", files, err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "pdf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected cross-type cache directory: %v", err)
	}
}

func TestExistingLocalFileWinsOverISBNClassification(t *testing.T) {
	t.Chdir(t.TempDir())
	const source = "9780306406157.txt"
	const content = "local evidence whose filename happens to be a valid ISBN\n"
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	h := mustNew(t, Options{CacheDir: t.TempDir(), Converter: &fakeConverter{}})
	result := h.Fetch(context.Background(), source)
	if result.Error != "" {
		t.Fatalf("existing local file was classified as a scholarly identifier: %s", result.Error)
	}
	if !strings.Contains(result.Content, "local evidence") {
		t.Fatalf("local content missing from result: %#v", result)
	}
}

func TestSearchSearxThenBraveFallback(t *testing.T) {
	searx := &recordingTransport{respond: func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusServiceUnavailable, "application/json", `{}`), nil
	}}
	brave := &recordingTransport{respond: func(r *http.Request) (*http.Response, error) {
		return jsonResponse(
			r,
			`{"web":{"results":[{"title":"Result","url":"https://example.test/r","description":"snippet"}]}}`,
		), nil
	}}
	got, backend, err := Search(
		context.Background(),
		"query",
		SearchOptions{
			SearXNGURL:  "https://search.test",
			BraveAPIKey: "key",
			SearXNG:     &http.Client{Transport: searx},
			Brave:       &http.Client{Transport: brave},
		},
	)
	if err != nil || backend != "brave" || len(got) != 1 || got[0].URL != "https://example.test/r" {
		t.Fatalf("Search() = %#v backend=%q err=%v", got, backend, err)
	}
}

func TestSearchLegacySingularEngineAndBraveLanguage(t *testing.T) {
	searx := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.URL.Query().Get("language"); got != "zh" {
			t.Errorf("SearXNG language=%q, want zh", got)
		}
		if got := request.URL.Query().Get("engines"); got != "baidu" {
			t.Errorf("SearXNG engines=%q, want baidu", got)
		}
		return jsonResponse(
			request,
			`{"results":[{"title":"B","url":"https://b.example","content":"snippet b","engine":"ddg"}]}`,
		), nil
	})}
	results, backend, err := searchConfigured(context.Background(), "fixture", SearchOptions{
		SearXNGURL: "https://search.example", Lang: "zh", Engines: "baidu", SearXNG: searx,
	})
	if err != nil || backend != "searxng" || len(results) != 1 {
		t.Fatalf("SearXNG result=%#v backend=%q err=%v", results, backend, err)
	}
	if results[0].Engine != "ddg" {
		t.Errorf("singular SearXNG engine=%q, want ddg", results[0].Engine)
	}

	brave := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.URL.Query().Get("search_lang"); got != "zh" {
			t.Errorf("Brave search_lang=%q, want zh", got)
		}
		return jsonResponse(
			request,
			`{"web":{"results":[{"title":"C","url":"https://c.example","description":"desc c"}]}}`,
		), nil
	})}
	results, backend, err = searchConfigured(context.Background(), "fixture", SearchOptions{
		BraveAPIKey: "example-fixture-key", Lang: "zh", Brave: brave,
	})
	if err != nil || backend != "brave" || len(results) != 1 || results[0].Engine != "brave" {
		t.Fatalf("Brave result=%#v backend=%q err=%v", results, backend, err)
	}
}

func TestArchiveRefusesTraversalAndSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostile.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"../escape.txt", "ok.txt"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, "content")
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ListArchive(path); err == nil || !strings.Contains(err.Error(), "..") {
		t.Fatalf("ListArchive traversal err = %v", err)
	}

	tarPath := filepath.Join(t.TempDir(), "safe.tar")
	tf, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(tf)
	data := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{Name: "ok.txt", Mode: 0o600, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tf.Close(); err != nil {
		t.Fatal(err)
	}
	members, err := ListArchive(tarPath)
	if err != nil || len(members) != 1 {
		t.Fatalf("tar list = %#v err=%v", members, err)
	}
	got, err := ReadArchiveMember(tarPath, "ok.txt")
	if err != nil || string(got) != "hello" {
		t.Fatalf("tar member = %q err=%v", got, err)
	}
}

func TestLocalDenyResolvedPaths(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "credentials.pem")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reason := DenyLocalPath(secret, nil); reason == "" {
		t.Fatal("credential path was allowed")
	}
	link := filepath.Join(root, "ordinary.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if reason := DenyLocalPath(link, []string{root}); reason == "" {
		t.Fatal("symlink to credential was allowed")
	}
	outside := filepath.Join(t.TempDir(), "ok.txt")
	if err := os.WriteFile(outside, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reason := DenyLocalPath(outside, []string{root}); reason == "" {
		t.Fatal("path outside configured root was allowed")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(r *http.Request, body string) *http.Response {
	return response(r, http.StatusOK, "application/json", body)
}

func TestCacheKeyUsesSHA1Suffix(t *testing.T) {
	key := "https://example.test/a?b=1"
	sum := sha1.Sum([]byte(key))
	want := hex.EncodeToString(sum[:])[:10]
	if got := CacheKey(key, "html"); !strings.HasSuffix(got, "__"+want+".md") {
		t.Fatalf("cache key = %q", got)
	}
}

func TestExtensionlessSniffedKindCachesAndJinaEnvelopeIsStripped(t *testing.T) {
	var calls int
	pdfTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nbody"), nil
	})
	h := mustNew(
		t,
		Options{
			CacheDir:  t.TempDir(),
			Client:    &http.Client{Transport: pdfTransport},
			Chrome:    &http.Client{Transport: pdfTransport},
			Converter: &fakeConverter{},
		},
	)
	first := h.Fetch(context.Background(), "https://example.test/download")
	if first.Error != "" || first.Kind != "pdf" {
		t.Fatalf("extensionless PDF = %#v", first)
	}
	second := h.Fetch(context.Background(), "https://example.test/download")
	if second.Error != "" || second.CacheStatus != "hit" || calls != 1 {
		t.Fatalf("sniffed cache = %#v calls=%d", second, calls)
	}

	direct := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", "checking your browser"), nil
	})
	jina := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/plain", "Title: T\nMarkdown Content:\n# Body\ntext"), nil
	})
	h = mustNew(
		t,
		Options{
			CacheDir: t.TempDir(),
			Client:   &http.Client{Transport: direct},
			Chrome:   &http.Client{Transport: direct},
			Jina:     &http.Client{Transport: jina},
		},
	)
	got := h.Fetch(context.Background(), "https://example.test/page")
	if got.Error != "" || got.Kind != "html" || got.Content != "# Body\ntext" {
		t.Fatalf("Jina envelope = %#v", got)
	}
}

func TestGoogleDriveFileViewFetchesCompleteDownloadInsteadOfPreviewHTML(t *testing.T) {
	const source = "https://drive.google.com/file/d/1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy/view"
	transport := &recordingTransport{respond: func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() == "drive.usercontent.google.com" {
			if got := request.URL.Query().Get("id"); got != "1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy" {
				t.Fatalf("download id = %q", got)
			}
			return response(request, http.StatusOK, "application/pdf", "%PDF-1.7\npage 1\npage 18\n%%EOF"), nil
		}
		return response(
			request,
			http.StatusOK,
			"text/html",
			strings.Repeat("<p>Drive preview page 1 through page 4 only</p>", 20),
		), nil
	}}
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: transport},
		Chrome:    &http.Client{Transport: transport},
		Converter: &fakeConverter{},
	})
	if _, err := h.cache.save(
		source,
		"html",
		"jina",
		strings.Repeat("Drive preview page 1 through page 4 only\n", 20),
		[]string{"direct", "chrome-impersonation", "jina"},
	); err != nil {
		t.Fatal(err)
	}

	result := h.Fetch(context.Background(), source)
	if result.Error != "" || result.Kind != "pdf" || result.Method != "google-drive-download" {
		t.Fatalf("Drive file result = %#v, want complete PDF download", result)
	}
	if result.Source != source {
		t.Fatalf("result source = %q, want original share URL", result.Source)
	}
	transport.mu.Lock()
	seen := append([]string(nil), transport.seen...)
	transport.mu.Unlock()
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "https://drive.usercontent.google.com/download?") {
		t.Fatalf("requests = %#v, want only the complete-file download endpoint", seen)
	}
}

func TestGoogleDriveDownloadURLRecognizesOnlyOwnedFileLinks(t *testing.T) {
	tests := []struct {
		source string
		wantID string
	}{
		{
			"https://drive.google.com/file/d/1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy/view?usp=sharing",
			"1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy",
		},
		{"https://drive.google.com/open?id=1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy", "1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy"},
		{"https://drive.google.com.evil.test/file/d/1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy/view", ""},
		{"https://drive.google.com/file/d/short/view", ""},
		{"https://drive.google.com/drive/folders/1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy", ""},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			target, ok := googleDriveDownloadURL(test.source)
			if test.wantID == "" {
				if ok || target != "" {
					t.Fatalf("googleDriveDownloadURL() = %q, %v; want no rewrite", target, ok)
				}
				return
			}
			if !ok {
				t.Fatal("googleDriveDownloadURL() did not recognize file link")
			}
			parsed, err := url.Parse(target)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Scheme != "https" || parsed.Hostname() != "drive.usercontent.google.com" ||
				parsed.Query().Get("id") != test.wantID {
				t.Fatalf("download target = %q", target)
			}
		})
	}
}

func TestTokenEstimateMatchesOracleRegimes(t *testing.T) {
	if got := estimateTokens(strings.Repeat("word ", 100)); got != 250 {
		t.Fatalf("prose tokens=%d", got)
	}
	if got := estimateTokens(strings.Repeat("{}[];", 100)); got != 278 {
		t.Fatalf("code tokens=%d", got)
	}
	if got := estimateTokens(strings.Repeat("漢", 10)); got != 13 {
		t.Fatalf("CJK tokens=%d", got)
	}
}

func TestChromeTransportUsesUTLSAndRejectsMixedDNSAnswers(t *testing.T) {
	h := mustNew(t, Options{CacheDir: t.TempDir(), ResolvePublic: func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("127.0.0.1")}, nil
	}})
	if !ChromeTransport(h.chrome) {
		t.Fatal("production Chrome client is not the uTLS transport")
	}
	req, err := http.NewRequest(http.MethodGet, "https://rebind.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.chrome.Do(req); err == nil || !strings.Contains(strings.ToLower(err.Error()), "private") {
		t.Fatalf("mixed DNS answer error=%v", err)
	}
}

func TestChromeHeadersMatchCapturedChrome146Profile(t *testing.T) {
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		want := map[string]string{
			"User-Agent":                chromeUA,
			"Accept-Language":           "en-US,en;q=0.9",
			"Accept-Encoding":           "gzip, deflate, br, zstd",
			"Priority":                  "u=0, i",
			"Sec-CH-UA":                 `"Chromium";v="146", "Not-A.Brand";v="24", "Google Chrome";v="146"`,
			"Sec-CH-UA-Platform":        `"macOS"`,
			"Sec-CH-UA-Mobile":          "?0",
			"Upgrade-Insecure-Requests": "1",
		}
		for key, value := range want {
			if got := r.Header.Get(key); got != value {
				t.Fatalf("Chrome header %s=%q want %q", key, got, value)
			}
		}
		if got := r.Header.Get("Cache-Control"); got != "" {
			t.Fatalf("Chrome unexpectedly sent Cache-Control=%q", got)
		}
		return response(r, http.StatusOK, "text/html", strings.Repeat("rich ", 120)), nil
	})
	client := &http.Client{Transport: &userAgentTransport{base: base, ua: chromeUA, chrome: true}}
	req, err := http.NewRequest(http.MethodGet, "https://example.test/headers", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveGzipAndCacheSearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("gzip member")
	if err := tw.WriteHeader(&tar.Header{Name: "docs/readme.txt", Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := ListArchive(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("gzip list=%#v err=%v", entries, err)
	}
	got, err := ReadArchiveMember(path, "docs/readme.txt")
	if err != nil || string(got) != "gzip member" {
		t.Fatalf("gzip read=%q err=%v", got, err)
	}
	c := newCache(t.TempDir(), time.Hour)
	if _, err := c.save("https://example.test/a", "html", "direct", "needle needle", nil); err != nil {
		t.Fatal(err)
	}
	hits, err := c.Search("needle", 10, true)
	if err != nil || len(hits) != 1 || hits[0].Matches != 2 {
		t.Fatalf("cache search=%#v err=%v", hits, err)
	}
}

func TestRedirectAndMetadataInjectionAreBlocked(t *testing.T) {
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://127.0.0.1/secret"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})
	h := mustNew(
		t,
		Options{
			CacheDir:  t.TempDir(),
			Client:    &http.Client{Transport: tr},
			Chrome:    &http.Client{Transport: tr},
			Jina:      &http.Client{Transport: tr},
			Converter: &fakeConverter{},
		},
	)
	result := h.Fetch(context.Background(), "https://example.test/redirect")
	if result.Error == "" || !strings.Contains(strings.ToLower(result.Error), "private") {
		t.Fatalf("redirect result=%#v", result)
	}
	path := filepath.Join(t.TempDir(), "meta.txt")
	if _, err := h.cache.save("https://example.test/a\nmethod: injected", "txt", "direct", "body", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(
		filepath.Join(h.options.CacheDir, CacheKey("https://example.test/a\nmethod: injected", "txt")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\nmethod: injected\n") {
		t.Fatalf("frontmatter injection survived: %q", raw)
	}
	_ = path
}

func TestArchiveNFCNormalizesAndReadsNFDName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nfc.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("e\u0301.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "normalized")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	members, err := ListArchive(path)
	if err != nil || len(members) != 1 || members[0].Name != "é.txt" {
		t.Fatalf("NFC listing = %#v err=%v", members, err)
	}
	body, err := ReadArchiveMember(path, "é.txt")
	if err != nil || string(body) != "normalized" {
		t.Fatalf("NFC read = %q err=%v", body, err)
	}
}

func TestGetBodyCapsAtMaxWithoutFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/plain", "0123456789abcdef"), nil
	})}
	body, status, contentType, err := getBody(context.Background(), client, "https://example.test/large", defaultUA, 10)
	if err != nil || status != http.StatusOK || contentType != "text/plain" || string(body) != "0123456789" {
		t.Fatalf("capped body=%q status=%d ct=%q err=%v", body, status, contentType, err)
	}
}

func TestDetectTablesMatchOracle(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"https://x.test/a.tar.gz", "tar"},
		{"https://x.test/a.tgz", "tar"},
		{"https://x.test/a.tar.bz2", "tar"},
		{"https://x.test/a.tbz2", "tar"},
		{"https://x.test/a.tar.xz", "tar"},
		{"https://x.test/a.txz", "tar"},
		{"https://x.test/a.gz", "tar"},
		{"https://x.test/a.bz2", "tar"},
		{"https://x.test/a.xz", "tar"},
		{"https://x.test/a.docx", "docx"},
		{"https://x.test/a.xlsx", "xlsx"},
		{"https://x.test/a.pptx", "pptx"},
		{"https://x.test/a.csv", "csv"},
		{"https://x.test/a.json", "json"},
		{"https://x.test/a.png", "image"},
		{"https://x.test/a.unknown", "html"},
	} {
		if got := DetectKind(tc.source); got != tc.want {
			t.Errorf("DetectKind(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
	for _, tc := range []struct {
		ct, head, want string
	}{
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "PK\x03\x04", "docx"},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "PK\x03\x04", "xlsx"},
		{"application/vnd.openxmlformats-officedocument.presentationml.presentation", "PK\x03\x04", "pptx"},
		{"application/json", "{}", "json"},
		{"text/csv", "a,b", "csv"},
		{"application/octet-stream", "%PDF-1.7", "pdf"},
		{"application/octet-stream", "Rar!\x1a\x07", "rar"},
		{"application/octet-stream", "\x1f\x8b\x08", "tar"},
		{"application/octet-stream", "BZh91", "tar"},
		{"application/octet-stream", "\xfd7zXZ\x00", "tar"},
		{"text/html", "PK\x03\x04", ""},
		{"text/plain", "hello", ""},
		{"", "\x89PNG\r\n\x1a\n", "image"},
	} {
		if got := SniffKind(tc.ct, []byte(tc.head)); got != tc.want {
			t.Errorf("SniffKind(%q,%q) = %q, want %q", tc.ct, tc.head, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name, ct, sample string
		want             bool
	}{
		{"book.text", "", "plain prose", true},
		{"README.rst", "", "plain prose", true},
		{"notes.log", "", "plain prose", true},
		{"doc.tex", "", "plain prose", true},
		{"doc.org", "", "plain prose", true},
		{"x", "text/x-markdown", "# heading", true},
		{"x", "text/x-rst", "heading\n=======", true},
		{"x.html", "", "prose", false},
		{"x", "text/plain", "<p class=\"x\">html</p>", false},
	} {
		if got := IsPlainText(tc.name, tc.ct, tc.sample); got != tc.want {
			t.Errorf("IsPlainText(%q,%q) = %v, want %v", tc.name, tc.ct, got, tc.want)
		}
	}
}

// TestClassifyKindOOXMLExtensionBeatsZipMagic pins the fetchLocal regression:
// every OOXML document (.docx/.xlsx/.pptx) IS a zip container, so its body
// starts with the "PK\x03\x04" zip signature. classifyKind's magic-byte sniff
// (net.go, right after the openxmlformats content-type/extension branches)
// must not shadow the file-extension classification that runs afterward —
// otherwise a local .docx reaches harvestpy's converter tagged "zip", and the
// converter refuses archive kinds outright (harvestpy/converter.go:84).
func TestClassifyKindOOXMLExtensionBeatsZipMagic(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	body := buf.Bytes()
	if !bytes.HasPrefix(body, []byte("PK\x03\x04")) {
		t.Fatalf("fixture is not a zip container: %q", body[:minInt(len(body), 4)])
	}
	for _, tc := range []struct{ name, want string }{
		{"report.docx", "docx"},
		{"report.xlsx", "xlsx"},
		{"report.pptx", "pptx"},
		{"report.zip", "zip"},
	} {
		if got := classifyKind(tc.name, "", body); got != tc.want {
			t.Errorf(
				"classifyKind(%q, ct=%q, body=OOXML-zip-magic) = %q, want %q — the zip magic-byte sniff must not shadow the file extension",
				tc.name,
				"",
				got,
				tc.want,
			)
		}
	}
}

func TestArchiveNameLimitCountsUnicodeRunesAfterNFC(t *testing.T) {
	if err := validateMemberName(strings.Repeat("é", 255)); err != nil {
		t.Fatalf("255 Unicode code points should pass: %v", err)
	}
	if err := validateMemberName(strings.Repeat("é", 256)); err == nil {
		t.Fatal("256 Unicode code points should fail")
	}
	// NFD input is normalized before the boundary check and remains a 255-rune name.
	if err := validateMemberName(strings.Repeat("e\u0301", 255)); err != nil {
		t.Fatalf("NFD 255-name should pass after NFC: %v", err)
	}
}

func TestFetchBareTitleRefusesToGuess(t *testing.T) {
	h := mustNew(
		t,
		Options{
			CacheDir: t.TempDir(),
			OA: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				t.Fatal("bare title must not query OA providers")
				return nil, nil
			})},
		},
	)
	got := h.Fetch(context.Background(), "A Mathematical Theory of Communication")
	if got.Error == "" || !strings.Contains(got.Error, "findWorks") {
		t.Fatalf("bare title result = %#v", got)
	}
}

func TestLocalizeImagesSkipsOverLimitResponse(t *testing.T) {
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "image/png", strings.Repeat("x", maxImageBytes+1)), nil
	})
	h := mustNew(
		t,
		Options{CacheDir: t.TempDir(), Client: &http.Client{Transport: tr}, Chrome: &http.Client{Transport: tr}},
	)
	markdown, err := h.LocalizeImages(
		context.Background(),
		"![figure](https://example.test/too-large.png)",
		"https://example.test/article",
	)
	if err != nil || !strings.Contains(markdown, "too-large.png") {
		t.Fatalf("localized over-limit image = %q err=%v", markdown, err)
	}
}
