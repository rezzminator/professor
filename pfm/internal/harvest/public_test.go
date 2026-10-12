package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const publicTestDOI = "10.1234/public.boundary"

func setHarvestTestJail(t *testing.T) {
	t.Helper()
	t.Setenv("TMUX_TMPDIR", t.TempDir())
}

func TestFetchPublicResolvesDOIIdentityAndISBNNamedLocalFile(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})
	article := strings.Repeat("cached DOI article body ", 30)
	cachedPath, err := h.cache.save(publicTestDOI, "html", "oa:fixture", article, 0, []string{"oa:fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, source := range []string{publicTestDOI, "https://doi.org/" + publicTestDOI} {
		got := h.FetchPublic(context.Background(), source, FetchOptions{})
		if got.Error != "" {
			t.Fatalf("FetchPublic(%q) error = %q", source, got.Error)
		}
		if got.Source != source || got.Path == cachedPath || got.Content == "" {
			t.Fatalf("FetchPublic(%q) = %#v; want public exported artifact", source, got)
		}
		if strings.Contains(got.Content, "oa:fixture") || strings.Contains(got.Content, cacheDir) {
			t.Fatalf("FetchPublic(%q) exposed private provenance: %q", source, got.Content)
		}
	}

	localRoot := t.TempDir()
	localPath := filepath.Join(localRoot, "9780306406157.txt")
	if err := os.WriteFile(localPath, []byte("local ISBN-named evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	localHarvester := mustNew(t, Options{CacheDir: t.TempDir(), LocalRoots: []string{localRoot}})
	local := localHarvester.FetchPublic(context.Background(), localPath, FetchOptions{})
	if local.Error != "" || !strings.Contains(local.Content, "local ISBN-named evidence") {
		t.Fatalf("FetchPublic(%q) = %#v; want the existing local file", localPath, local)
	}
}

func TestFetchPublicSelectedURLsWithSameDOIKeepTheirOwnArtifact(t *testing.T) {
	setHarvestTestJail(t)
	withPublicDNSForProviderTest(t)
	seen := []string{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.URL.String())
		body := "%PDF-1.7\n"
		switch r.URL.Path {
		case "/repository-a/10.1234/public.boundary.pdf":
			body += "repository-a\n%%EOF"
		case "/repository-b/10.1234/public.boundary.pdf":
			body += "repository-b\n%%EOF"
		default:
			return response(r, http.StatusNotFound, "text/plain", "missing"), nil
		}
		return response(r, http.StatusOK, "application/pdf", body), nil
	})}
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   client,
		Chrome:   client,
		Converter: legacyConverterFunc(
			func(_ context.Context, _, _ string, body []byte) (string, error) { return string(body), nil },
		),
	})
	for _, tc := range []struct {
		url, marker string
	}{
		{"https://repository.test/repository-a/10.1234/public.boundary.pdf", "repository-a"},
		{"https://repository.test/repository-b/10.1234/public.boundary.pdf", "repository-b"},
	} {
		handle, err := h.PublicHandle(tc.url)
		if err != nil {
			t.Fatalf("PublicHandle(%q): %v", tc.url, err)
		}
		got := h.FetchPublic(context.Background(), handle, FetchOptions{})
		if got.Error != "" || !strings.Contains(got.Content, tc.marker) {
			t.Fatalf("FetchPublic(%q) = %#v; want %q", handle, got, tc.marker)
		}
	}
	if len(seen) != 2 || seen[0] != "https://repository.test/repository-a/10.1234/public.boundary.pdf" ||
		seen[1] != "https://repository.test/repository-b/10.1234/public.boundary.pdf" {
		t.Fatalf("selected URL requests = %#v; want each exact repository URL", seen)
	}
}

func TestPublicResultKeepsCompleteArtifactAndFetchedAtWithoutProvenance(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir, MaxInlineChars: 32})
	privatePath := filepath.Join(cacheDir, CacheKey("10.1234/private", "html"))
	if err := os.MkdirAll(filepath.Dir(privatePath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "**Source:** https://mirror.secret.example/private\n---\n\n" + strings.Repeat(
		"article body ",
		20,
	) + "TAIL_SENTINEL"
	raw := "---\nurl: https://mirror.secret.example/private\nfetched_at: 2025-01-02T03:04:05Z\nsource: harvester\nmethod: doi-mirror\nrungs: direct, mirror\n---\n\n" + body
	if err := os.WriteFile(privatePath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	got := h.PublicResult("10.1234/private", Result{
		Source:      "10.1234/private",
		Kind:        "html",
		Path:        privatePath,
		Method:      "doi-mirror",
		Rungs:       []string{"direct", "mirror"},
		CacheStatus: "hit",
	}, false)
	if got.Error != "" {
		t.Fatalf("PublicResult() error = %q", got.Error)
	}
	if got.Method != "mirror" || len(got.Rungs) != 0 || strings.Contains(got.Content, "mirror.secret.example") ||
		strings.Contains(got.Content, cacheDir) {
		t.Fatalf("public result leaked private fields: %#v", got)
	}
	if len(got.Content) >= len(body) || strings.Contains(got.Content, "TAIL_SENTINEL") {
		t.Fatalf("inline content was not capped: %q", got.Content)
	}
	complete, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	completeText := string(complete)
	if !strings.Contains(completeText, "TAIL_SENTINEL") || strings.Contains(completeText, "mirror.secret.example") ||
		!strings.Contains(completeText, "fetched_at: 2025-01-02T03:04:05Z") {
		t.Fatalf("public artifact was incomplete or rewrote metadata: %q", completeText)
	}

	sizeOnly := h.PublicResult(
		"10.1234/private",
		Result{Source: "10.1234/private", Kind: "html", Path: privatePath},
		true,
	)
	if sizeOnly.Error != "" || sizeOnly.Content != "" || sizeOnly.Bytes == 0 || sizeOnly.Path == "" {
		t.Fatalf("size-only public result = %#v", sizeOnly)
	}
}

func TestPublicFailuresDistinguishOutageFromMissingWithoutRawProviderDetails(t *testing.T) {
	setHarvestTestJail(t)
	for _, tc := range []struct {
		name, kind, want string
	}{
		{"outage", "connect", "connection failed"},
		{"missing", "missing", "not found"},
		{"challenge", "challenge", "access challenge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PublicFailure("10.1234/public.boundary", Result{
				Source:    "https://mirror.secret.example/private",
				Error:     "GET https://mirror.secret.example/private: provider internals",
				ErrorKind: tc.kind,
			})
			if !strings.Contains(strings.ToLower(got.Error), tc.want) ||
				strings.Contains(got.Error, "mirror.secret.example") ||
				strings.Contains(got.Error, "provider internals") {
				t.Fatalf("public failure = %#v; want safe %q", got, tc.want)
			}
		})
	}
}

func TestPublicResultDoesNotAcceptNonHarvesterProvenanceArtifact(t *testing.T) {
	setHarvestTestJail(t)
	h := mustNew(t, Options{CacheDir: t.TempDir()})
	path := filepath.Join(h.options.CacheDir, "html", "foreign.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte("---\nurl: https://mirror.secret.example/private\nmethod: foreign\n---\n\nbody"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	got := h.PublicResult("10.1234/public.boundary", Result{Kind: "html", Path: path}, false)
	if got.Error == "" || !strings.Contains(strings.ToLower(got.Error), "stored") ||
		strings.Contains(got.Error, "mirror.secret.example") {
		t.Fatalf("foreign artifact result = %#v; want safe refusal", got)
	}
	if strings.Contains(strings.ToLower(got.Error), "not found") {
		t.Fatalf("foreign artifact was misreported missing: %q", got.Error)
	}
}

// TestJSONResultsCarriesGapsAndVia pins the `pfm harvest --json` object:
// `gaps` and `via` are always present (gaps an empty list when complete, one
// entry per joined reason otherwise), so a caller reads completeness and the
// storing rung from fields, never from the markdown marker; the embedded
// result's own `method` and `partial` keys never render beside them.
func TestJSONResultsCarriesGapsAndVia(t *testing.T) {
	t.Parallel()
	complete := Result{
		Source: "https://fixture.example/a", Kind: "html", Method: "browser-chrome",
		CacheStatus: "hit", HTTPStatus: 200,
	}
	partial := complete
	partial.Partial = joinReasons("page 3 of 9 failed to convert", "1 image(s) could not be published")
	encoded, err := json.Marshal(JSONResults([]Result{complete, partial}))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
	for index, want := range [][]any{{}, {"page 3 of 9 failed to convert", "1 image(s) could not be published"}} {
		got, ok := decoded[index]["gaps"]
		if !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("result %d gaps=%v (present=%t), want %q: %s", index, got, ok, want, encoded)
		}
		if decoded[index]["via"] != "browser-chrome" {
			t.Fatalf("result %d via=%v, want browser-chrome: %s", index, decoded[index]["via"], encoded)
		}
		// One shadow mechanism hides both embedded keys; method's absence proves it.
		if _, found := decoded[index]["method"]; found {
			t.Fatalf("result %d still carries the embedded method key: %s", index, encoded)
		}
		// The MCP read item's words: cached and status, never cache_status or http_status.
		if decoded[index]["cached"] != true || decoded[index]["status"] != float64(200) {
			t.Fatalf("result %d cached=%v status=%v, want true and 200: %s",
				index, decoded[index]["cached"], decoded[index]["status"], encoded)
		}
		for _, old := range []string{"cache_status", "http_status", "partial"} {
			if _, found := decoded[index][old]; found {
				t.Fatalf("result %d carries the old key %q: %s", index, old, encoded)
			}
		}
	}
}

// frontmatterOf reads path and parses it with the one parser.
func frontmatterOf(t *testing.T, path string) (map[string]string, string, string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	meta, body := readFrontmatter(string(raw))
	return meta, body, string(raw)
}

// TestBothStoresWriteTheirFrontmatterAndBodiesCarryNone: what the harvester
// knows about an artifact (where it came from, what the converter read about
// it, what it is missing) is frontmatter in both stores, through one writer;
// the body is the document alone. The public store is the privacy projection
// of the same record (README § Public results): its rung class as via, never
// the method, the rung trace or the site name a mirror's markup carries.
// Page-sourced title and author values cannot write a key of their own.
func TestBothStoresWriteTheirFrontmatterAndBodiesCarryNone(t *testing.T) {
	setHarvestTestJail(t)
	h := mustNew(t, Options{CacheDir: t.TempDir()})
	const source = "https://example.test/notes"
	const body = "# Gauge notes\n\nThe body.\n"
	content := withPartial(WithConverterMeta(body, map[string]string{
		"title": "Gauge notes\nmethod: injected", "author": "Ada\nvia: forged", "published": "2024-05-06",
		"site": "Mirror Site", "license": "CC BY", "transformed": "json-outline",
	}), "login wall")
	stored := h.storeResult(source, "html", "browser-chrome", content, 10, 200,
		[]string{"direct", "browser-chrome"}, FetchOptions{})
	if stored.Error != "" {
		t.Fatalf("storeResult error = %q", stored.Error)
	}
	if stored.Content != body || stored.Partial != "login wall" {
		t.Errorf("stored content %q partial %q, want the bare body and its gaps", stored.Content, stored.Partial)
	}
	private, privateBody, privateRaw := frontmatterOf(t, stored.Path)
	wantPrivate := map[string]string{
		"source":      "harvester",
		"url":         source,
		"kind":        "html",
		"method":      "browser-chrome",
		"rungs":       "direct, browser-chrome",
		"http_status": "200",
		"title":       "Gauge notes\nmethod: injected",
		"author":      "Ada\nvia: forged",
		"published":   "2024-05-06",
		"site":        "Mirror Site",
		"license":     "CC BY",
		"chars": strconv.Itoa(
			contentChars(body),
		),
		"token_count": strconv.Itoa(EstimateTokens(body)),
		"gaps":        "login wall",
		"transformed": "json-outline",
	}
	for key, value := range wantPrivate {
		if private[key] != value {
			t.Errorf("private %s = %q, want %q", key, private[key], value)
		}
	}
	if private["fetched_at"] == "" || privateBody != body {
		t.Errorf("private fetched_at %q body %q, want a stamp and the bare body", private["fetched_at"], privateBody)
	}

	public := h.exportResult(source, "urls", stored, false)
	if public.Error != "" {
		t.Fatalf("exportResult error = %q", public.Error)
	}
	meta, publicBody, publicRaw := frontmatterOf(t, public.Path)
	wantPublic := map[string]string{
		"source":      "harvester",
		"request":     source,
		"field":       "urls",
		"url":         source,
		"kind":        "html",
		"via":         "browser-chrome",
		"http_status": "200",
		"fetched_at":  private["fetched_at"],
		"title":       "Gauge notes\nmethod: injected",
		"author":      "Ada\nvia: forged",
		"published":   "2024-05-06",
		"license":     "CC BY",
		"chars":       strconv.Itoa(contentChars(body)),
		"token_count": strconv.Itoa(EstimateTokens(body)),
		"gaps":        "login wall",
		"transformed": "json-outline",
	}
	for key, value := range wantPublic {
		if meta[key] != value {
			t.Errorf("public %s = %q, want %q", key, meta[key], value)
		}
	}
	for _, private := range []string{"method", "rungs", "site"} {
		if _, ok := meta[private]; ok {
			t.Errorf("public frontmatter carries the private key %q: %v", private, meta)
		}
	}
	if publicBody != body || public.Content != body || public.Partial != "login wall" {
		t.Errorf("public body %q content %q partial %q, want the bare body and its gaps",
			publicBody, public.Content, public.Partial)
	}
	for _, raw := range []string{privateRaw, publicRaw} {
		for _, forged := range []string{"\nmethod: injected\n", "\nvia: forged\n"} {
			if strings.Contains(raw, forged) {
				t.Errorf("a page-sourced value wrote its own key %q:\n%s", forged, raw)
			}
		}
	}
}

// TestLegacyArtifactsReadWithTheirBannerAndMetadataLifted: a cache entry
// written before frontmatter carried gaps and the converter's metadata keeps
// its partial banner and **Title:** block in the body. Read back, it is partial
// for the same reason, its body is the document alone, and the public copy
// carries the lifted gaps and title in frontmatter.
func TestLegacyArtifactsReadWithTheirBannerAndMetadataLifted(t *testing.T) {
	setHarvestTestJail(t)
	cacheDir := t.TempDir()
	h := mustNew(t, Options{CacheDir: cacheDir})
	const source = "https://example.test/legacy"
	document := "# Legacy page\n\n" + strings.Repeat("The stilling well agrees with the staff gauge. ", 6) + "\n"
	path := filepath.Join(cacheDir, CacheKey(source, "html"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := "---\nurl: " + source + "\nfetched_at: 2099-01-02T03:04:05Z\nsource: harvester\nmethod: direct\n" +
		"token_count: 9\n---\n\n" + partialMarkerPrefix + "old gap\n\n**Title:** Old title\n**Source:** Old site\n\n---\n\n" +
		document
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	content, meta, loaded, ok := h.cache.load(source, "html")
	if !ok {
		t.Fatal("the legacy entry did not load")
	}
	result := h.resultFromCache(source, "html", content, meta, loaded)
	if result.Partial != "old gap" || result.Content != document {
		t.Errorf(
			"legacy read partial %q content %q, want the old gap and the bare document",
			result.Partial,
			result.Content,
		)
	}
	public := h.exportResult(source, "urls", result, false)
	if public.Error != "" {
		t.Fatalf("exportResult error = %q", public.Error)
	}
	publicMeta, publicBody, _ := frontmatterOf(t, public.Path)
	if publicMeta["gaps"] != "old gap" || publicMeta["title"] != "Old title" || publicMeta["site"] != "" ||
		publicMeta["fetched_at"] != "2099-01-02T03:04:05Z" || publicBody != document {
		t.Errorf("legacy public copy meta %v body %q, want gaps, title and stamp lifted, no site, the bare document",
			publicMeta, publicBody)
	}
}

// TestWithReasonMergesWholeReasons: a reason joins the gaps unless one of them
// is that same reason; one reason's words inside another never count.
func TestWithReasonMergesWholeReasons(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ reasons, reason, want string }{
		{
			"12 image(s) could not be published", "2 image(s) could not be published",
			"12 image(s) could not be published; 2 image(s) could not be published",
		},
		{
			"login wall; 3 of 9 comments loaded", "login wall; lazy content still loading",
			"login wall; 3 of 9 comments loaded; lazy content still loading",
		},
		{"login wall; 3 of 9 comments loaded", "3 of 9 comments loaded", "login wall; 3 of 9 comments loaded"},
		{"", "login wall", "login wall"},
		{"login wall", "", "login wall"},
	} {
		if got := withReason(tc.reasons, tc.reason); got != tc.want {
			t.Errorf("withReason(%q, %q) = %q, want %q", tc.reasons, tc.reason, got, tc.want)
		}
	}
}
