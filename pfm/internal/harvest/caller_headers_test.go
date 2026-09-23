package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// probeValue is the sentinel caller-header value: it must reach the target's
// origin and never a log, an error, a cache artifact or the output.
const probeValue = "sentinel-7f3a9c"

// headerSeen records, per host, the X-Probe and User-Agent each request
// carried.
type headerSeen struct {
	mu     sync.Mutex
	probes map[string][]string
	agents map[string][]string
	count  int
}

func newHeaderSeen() *headerSeen {
	return &headerSeen{probes: map[string][]string{}, agents: map[string][]string{}}
}

func (s *headerSeen) record(request *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	s.probes[request.URL.Host] = append(s.probes[request.URL.Host], request.Header.Get("X-Probe"))
	s.agents[request.URL.Host] = append(s.agents[request.URL.Host], request.Header.Get("User-Agent"))
}

func (s *headerSeen) probed(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range s.probes[host] {
		if value == probeValue {
			return true
		}
	}
	return false
}

func probeLines(extra ...string) *HeaderLines {
	return &HeaderLines{lines: append([]string{"X-Probe: " + probeValue}, extra...)}
}

// captureLogs gathers the obs records and the standard log output of a test.
func captureLogs(t *testing.T) (context.Context, func() string) {
	t.Helper()
	ctx, recorder := obs.Test(t)
	var std bytes.Buffer
	log.SetOutput(&std)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return ctx, func() string { return recorder.Raw() + std.String() }
}

// TestCallerHeadersReachTheTargetNeverTheReader: the caller's header reaches
// the direct and Chrome-impersonation rungs and never the reader service; the
// page the reader served names the headerless rung in partial, and the value
// is in no log, result or cache artifact.
func TestCallerHeadersReachTheTargetNeverTheReader(t *testing.T) {
	ctx, logs := captureLogs(t)
	seen := newHeaderSeen()
	walled := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen.record(r)
		return response(r, http.StatusForbidden, "text/html", "<html><body>checking your browser</body></html>"), nil
	})
	jina := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen.record(r)
		return response(r, http.StatusOK, "text/plain", strings.Repeat("rich article text ", 60)), nil
	})
	var chromeSeen bool
	chrome := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		chromeSeen = chromeSeen || r.Header.Get("X-Probe") == probeValue
		return walled(r)
	})
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: walled},
		Chrome:    &http.Client{Transport: chrome},
		Jina:      &http.Client{Transport: jina},
		Converter: &fakeConverter{},
	})
	got := probeLines().Fetch(ctx, h, "https://example.test/article", FetchOptions{})
	if got.Error != "" || got.Method != "jina" {
		t.Fatalf("Fetch = method %q error %q, want the jina rung", got.Method, got.Error)
	}
	if !seen.probed("example.test") || !chromeSeen {
		t.Fatalf("the caller header did not reach direct and Chrome: direct=%v chrome=%v",
			seen.probed("example.test"), chromeSeen)
	}
	if seen.probed("r.jina.ai") {
		t.Fatal("the caller header reached the reader service")
	}
	if !strings.Contains(got.Partial, "without the caller's headers") {
		t.Fatalf("partial = %q, want the headerless jina rung named", got.Partial)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read the cache artifact: %v", err)
	}
	for surface, text := range map[string]string{"output": string(encoded), "cache": string(stored), "logs": logs()} {
		if strings.Contains(text, probeValue) {
			t.Fatalf("the header value appears in the %s: %q", surface, text)
		}
	}
}

// TestCallerHeadersStayOnTheTargetOrigin: a redirect to another origin
// carries none of the caller's headers and gets the default User-Agent back;
// on the target's origin a caller User-Agent overrides the default.
func TestCallerHeadersStayOnTheTargetOrigin(t *testing.T) {
	seen := newHeaderSeen()
	site := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen.record(r)
		if r.URL.Host == "example.test" {
			moved := response(r, http.StatusFound, "text/html", "")
			moved.Header.Set("Location", "https://elsewhere.test/page")
			return moved, nil
		}
		return response(
			r,
			http.StatusOK,
			"text/html",
			"<html><body>"+strings.Repeat("<p>body text</p>", 80)+"</body></html>",
		), nil
	})
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: site},
		Chrome:    &http.Client{Transport: site},
		Converter: &fakeConverter{},
	})
	_ = probeLines(
		"User-Agent: caller-agent/1",
	).Fetch(context.Background(), h, "https://example.test/moved", FetchOptions{})
	if !seen.probed("example.test") || seen.agents["example.test"][0] != "caller-agent/1" {
		t.Fatalf("target origin: probe=%v agents=%v, want the caller's header and User-Agent",
			seen.probed("example.test"), seen.agents["example.test"])
	}
	if len(seen.probes["elsewhere.test"]) == 0 {
		t.Fatal("the redirect was not followed")
	}
	if seen.probed("elsewhere.test") {
		t.Fatal("a redirect to another origin carried the caller's header")
	}
	for _, agent := range seen.agents["elsewhere.test"] {
		if agent == "caller-agent/1" {
			t.Fatal("a redirect to another origin carried the caller's User-Agent")
		}
	}
}

// TestCallerHeadersRefusedAtEntry: each breach is a named error that never
// carries the value, and no request is sent.
func TestCallerHeadersRefusedAtEntry(t *testing.T) {
	many := map[string]string{}
	for index := range 33 {
		many[fmt.Sprintf("X-H%d", index)] = probeValue
	}
	for name, raw := range map[string]map[string]string{
		"crlf":           {"X-Probe": probeValue + "\r\nX-Evil: 1"},
		"bad name":       {"Bad Name": probeValue},
		"host":           {"Host": probeValue},
		"content-length": {"content-length": probeValue},
		"proxy":          {"Proxy-Authorization": probeValue},
		"33 headers":     many,
		"over 8 KiB":     {"X-Big": probeValue + strings.Repeat("a", 8<<10)},
	} {
		_, err := ParseCallerHeaders(raw)
		if !errors.Is(err, ErrCallerHeader) {
			t.Fatalf("%s: err = %v, want ErrCallerHeader", name, err)
		}
		if strings.Contains(err.Error(), probeValue) {
			t.Fatalf("%s: the error carries the value: %v", name, err)
		}
	}
	seen := newHeaderSeen()
	counting := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen.record(r)
		return response(r, http.StatusOK, "text/html", "<html></html>"), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(), Client: &http.Client{Transport: counting}, Chrome: &http.Client{Transport: counting},
		Jina: &http.Client{Transport: counting}, Converter: &fakeConverter{},
	})
	refused := (&HeaderLines{lines: []string{"Host: " + probeValue}}).Fetch(
		context.Background(), h, "https://example.test/a", FetchOptions{})
	noOrigin := probeLines().Fetch(context.Background(), h, "10.1038/nature14539", FetchOptions{})
	for _, got := range []Result{refused, noOrigin} {
		if got.Error == "" || strings.Contains(got.Error, probeValue) {
			t.Fatalf("refused set: error %q, want a named error without the value", got.Error)
		}
	}
	if seen.count != 0 {
		t.Fatalf("a refused header set sent %d request(s)", seen.count)
	}
}

// TestCallerHeadersPartitionTheCache: the same URL read with and without a
// header is two cache entries; the same header again is a cache hit.
func TestCallerHeadersPartitionTheCache(t *testing.T) {
	seen := newHeaderSeen()
	site := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen.record(r)
		return response(
			r,
			http.StatusOK,
			"text/html",
			"<html><body>"+strings.Repeat("<p>body text</p>", 80)+"</body></html>",
		), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(), Client: &http.Client{Transport: site}, Chrome: &http.Client{Transport: site},
		Converter: &fakeConverter{},
	})
	ctx, source := context.Background(), "https://example.test/cached"
	plain := h.Fetch(ctx, source)
	first := probeLines().Fetch(ctx, h, source, FetchOptions{})
	second := probeLines().Fetch(ctx, h, source, FetchOptions{})
	again := h.Fetch(ctx, source)
	if plain.Error != "" || first.Error != "" {
		t.Fatalf("reads failed: %q / %q", plain.Error, first.Error)
	}
	if seen.count != 2 || plain.Path == first.Path {
		t.Fatalf("requests = %d, paths %q / %q: want two entries, one request each", seen.count, plain.Path, first.Path)
	}
	if second.CacheStatus != cacheStatusHit || again.CacheStatus != cacheStatusHit {
		t.Fatalf(
			"repeat reads: with header %q, without %q; want both cache hits",
			second.CacheStatus,
			again.CacheStatus,
		)
	}
}

// TestCallerHeadersReachTheFileTargetNeverWayback: a download sends the
// caller's header to the target and never to the Wayback copy, and names the
// headerless rung in partial.
func TestCallerHeadersReachTheFileTargetNeverWayback(t *testing.T) {
	seen := newHeaderSeen()
	site := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen.record(r)
		if r.URL.Host == "web.archive.org" {
			return response(r, http.StatusOK, "image/png", retrievePNG), nil
		}
		return response(r, http.StatusNotFound, "text/html", "<html>gone</html>"), nil
	})
	wayback := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen.record(r)
		return jsonResponse(
			r,
			`{"archived_snapshots":{"closest":{"available":true,"timestamp":"20260901000000"}}}`,
		), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(), Client: &http.Client{Transport: site}, Chrome: &http.Client{Transport: site},
		OA: &http.Client{Transport: wayback},
	})
	headers, err := probeLines().Parse()
	if err != nil {
		t.Fatal(err)
	}
	scoped, ctx, err := h.ForCaller(context.Background(), headers, "https://203.0.113.10/figure.png")
	if err != nil {
		t.Fatal(err)
	}
	got := headers.MarkHeaderless(scoped.Download(ctx, "https://203.0.113.10/figure.png"))
	if got.Error != "" || got.Method != rungWayback {
		t.Fatalf("Download = method %q error %q, want wayback", got.Method, got.Error)
	}
	if !seen.probed("203.0.113.10") || seen.probed("web.archive.org") || seen.probed("archive.org") {
		t.Fatalf("probe reached: target=%v wayback=%v", seen.probed("203.0.113.10"), seen.probed("web.archive.org"))
	}
	if !strings.Contains(got.Partial, "without the caller's headers") {
		t.Fatalf("partial = %q, want the headerless wayback rung named", got.Partial)
	}
}
