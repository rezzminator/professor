package harvestcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// fixtureSite is an httptest server every harvester transport is routed to:
// /figure.png is a PNG, /wayback answers "no snapshot", anything else is 404.
// It records each request's path and X-Probe header; a lookup through the
// public resolver fails the test, so no rung leaves for the real network.
type fixtureSite struct {
	server *httptest.Server
	mu     sync.Mutex
	hits   []string
	probes map[string]string
}

func newFixtureSite(t *testing.T) *fixtureSite {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatalf("encode fixture png: %v", err)
	}
	site := &fixtureSite{probes: map[string]string{}}
	site.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site.mu.Lock()
		site.hits = append(site.hits, r.URL.Path)
		site.probes[r.URL.Path] = r.Header.Get("X-Probe")
		site.mu.Unlock()
		switch {
		case r.URL.Path == "/figure.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(encoded.Bytes())
		case strings.Contains(r.URL.Path, "wayback") || strings.Contains(r.URL.Path, "available"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"archived_snapshots":{}}`))
		default:
			http.Error(w, "gone", http.StatusNotFound)
		}
	}))
	t.Cleanup(site.server.Close)
	target, err := url.Parse(site.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// One client per slot: harvest.New reads a Chrome or OA client that aliases
	// Client as "unspecified" and swaps in a production client, which resolves
	// the fixture host and archive.org through the real public resolver.
	fixtureClient := func() *http.Client {
		return &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
			clone := r.Clone(r.Context())
			clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
			return http.DefaultTransport.RoundTrip(clone)
		})}
	}
	previous := newHarvester
	newHarvester = func(runtime harvestmcp.Runtime) (*harvest.Harvester, error) {
		return harvest.New(harvest.Options{
			CacheDir: runtime.CacheDir, Client: fixtureClient(), Chrome: fixtureClient(), Jina: fixtureClient(),
			OA: fixtureClient(),
			ResolvePublic: func(_ context.Context, host string) ([]net.IP, error) {
				t.Errorf(
					"a harvester client resolved %q through the public resolver; every rung must reach the fixture",
					host,
				)
				return nil, fmt.Errorf("fixture: public resolve of %q refused", host)
			},
		})
	}
	t.Cleanup(func() { newHarvester = previous })
	return site
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (site *fixtureSite) requests() int {
	site.mu.Lock()
	defer site.mu.Unlock()
	return len(site.hits)
}

func (site *fixtureSite) probe(path string) string {
	site.mu.Lock()
	defer site.mu.Unlock()
	return site.probes[path]
}

func downloadRuntime(t *testing.T) pfmconfig.Runtime {
	t.Helper()
	home := t.TempDir()
	return pfmconfig.Runtime{Paths: paths.Values{Home: home}, Config: pfmconfig.Config{Harvester: askHarvester(home)}}
}

// TestDownloadPrintsPathKindTypeAndSize: each URL's receipt names the stored
// file's path, kind, content type and size, in input order; a failing URL is a
// named ERROR line and a non-zero exit, never a silent skip; a --header
// reaches the target.
func TestDownloadPrintsPathKindTypeAndSize(t *testing.T) {
	site := newFixtureSite(t)
	var stdout, stderr bytes.Buffer
	code := Harvest([]string{
		"download-file", "--header", "X-Probe: fixture-token",
		"https://example.test/figure.png", "https://example.test/missing.bin",
	}, &stdout, &stderr, downloadRuntime(t))
	if code != 1 {
		t.Fatalf(
			"download-file code=%d, want 1 for the failed item; stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	out := stdout.String()
	head, failed, found := strings.Cut(out, "# https://example.test/missing.bin\nERROR: ")
	if !found || strings.TrimSpace(failed) == "" {
		t.Fatalf("the failing URL is not a named ERROR receipt:\n%s", out)
	}
	if !strings.HasPrefix(head, "# https://example.test/figure.png\npath: ") ||
		!strings.Contains(head, " / kind: png / content_type: image/png / bytes: ") {
		t.Fatalf("download receipt lacks path, kind, content type or size:\n%s", out)
	}
	pathField := strings.TrimPrefix(strings.SplitN(head, "\n", 3)[1], "path: ")
	pathField, _, _ = strings.Cut(pathField, " / ")
	info, err := os.Stat(pathField)
	if err != nil {
		t.Fatalf("printed path %q is not the stored file: %v", pathField, err)
	}
	if want := " / bytes: " + strconv.FormatInt(info.Size(), 10); !strings.Contains(head, want) {
		t.Fatalf("receipt size does not match the stored file (%s):\n%s", want, out)
	}
	if got := site.probe("/figure.png"); got != "fixture-token" {
		t.Fatalf("X-Probe at the target = %q, want the --header value", got)
	}
	if strings.Contains(out+stderr.String(), "fixture-token") {
		t.Fatalf("a header value leaked into the output:\n%s\n%s", out, stderr.String())
	}
}

// TestDownloadJSONCarriesTheSameFields: --json gives source, path, kind, via,
// content_type and bytes per item, and a failed item's error.
func TestDownloadJSONCarriesTheSameFields(t *testing.T) {
	newFixtureSite(t)
	var stdout, stderr bytes.Buffer
	code := Harvest(
		[]string{"download-file", "--json", "https://example.test/figure.png", "https://example.test/missing.bin"},
		&stdout,
		&stderr,
		downloadRuntime(t),
	)
	if code != 1 {
		t.Fatalf("download-file --json code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var items []struct {
		Source      string `json:"source"`
		Path        string `json:"path"`
		Kind        string `json:"kind"`
		ContentType string `json:"content_type"`
		Bytes       int64  `json:"bytes"`
		Via         string `json:"via"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("download-file --json is not a JSON array: %v\n%s", err, stdout.String())
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want two in input order", items)
	}
	first, second := items[0], items[1]
	if first.Source != "https://example.test/figure.png" || filepath.Base(first.Path) == "" || first.Kind != "png" ||
		first.ContentType != "image/png" || first.Bytes <= 0 || first.Via == "" || first.Error != "" {
		t.Fatalf("first item = %+v", first)
	}
	if strings.Contains(stdout.String(), `"method"`) {
		t.Fatalf("download-file --json names method; the field is via:\n%s", stdout.String())
	}
	if second.Source != "https://example.test/missing.bin" || second.Error == "" || second.Path != "" {
		t.Fatalf("failed item = %+v, want a named error and no path", second)
	}
}

// TestDownloadRefusesABadHeaderBeforeAnyRequest: a header the MCP tool refuses
// is refused here too, by name, with usage exit 2 and nothing sent.
func TestDownloadRefusesABadHeaderBeforeAnyRequest(t *testing.T) {
	for _, line := range []string{"Host: other.example", "no colon here", "Bad Name: x", "X-A: 1\r\nX-B: 2"} {
		site := newFixtureSite(t)
		var stdout, stderr bytes.Buffer
		code := Harvest([]string{"download-file", "--header", line, "https://example.test/figure.png"},
			&stdout, &stderr, downloadRuntime(t))
		if code != 2 || !strings.Contains(stderr.String(), harvest.ErrCallerHeader.Error()) {
			t.Fatalf("--header %q: code=%d stderr=%q, want 2 and the named refusal", line, code, stderr.String())
		}
		if site.requests() != 0 || stdout.Len() != 0 {
			t.Fatalf("--header %q: %d requests sent, stdout=%q; want none", line, site.requests(), stdout.String())
		}
	}
}

// TestDownloadJSONNamesTheErrorKind: a failed item carries the core's error
// kind — a refused share link is login, a missing file its own kind — never
// an absent error_kind beside an error.
func TestDownloadJSONNamesTheErrorKind(t *testing.T) {
	site := newFixtureSite(t)
	var stdout, stderr bytes.Buffer
	code := Harvest(
		[]string{
			"download-file",
			"--json",
			"https://mega.nz/file/AbCdEfGh#ExampleKey",
			"https://example.test/missing.bin",
		},
		&stdout,
		&stderr,
		downloadRuntime(t),
	)
	if code != 1 {
		t.Fatalf("download-file --json code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("download-file --json is not a JSON array: %v\n%s", err, stdout.String())
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want two", items)
	}
	if items[0]["error_kind"] != "login" || !strings.Contains(items[0]["error"].(string), "MEGA") {
		t.Errorf("share-link item = %+v, want error_kind login and the MEGA refusal", items[0])
	}
	if kind, _ := items[1]["error_kind"].(string); kind == "" || items[1]["error"] == nil {
		t.Errorf("missing item = %+v, want a named error and its error_kind", items[1])
	}
	if site.probe("/file/AbCdEfGh") != "" {
		t.Error("the refused share link was fetched")
	}
}
