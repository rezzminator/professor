package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
	cachedPath, err := h.cache.save(publicTestDOI, "html", "oa:fixture", article, 0, []string{"oa:fixture"})
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

// TestJSONResultsCarriesPartialAndMethod pins the `pfm harvest --json`
// object: `partial` and `method` are always present, so a caller reads
// completeness and the storing rung from fields, never from the markdown
// marker.
func TestJSONResultsCarriesPartialAndMethod(t *testing.T) {
	complete := Result{Source: "https://fixture.example/a", Kind: "html", Method: "browser-chrome"}
	partial := complete
	partial.Partial = "page 3 of 9 failed to convert"
	encoded, err := json.Marshal(JSONResults([]Result{complete, partial}))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
	for index, want := range []string{"", partial.Partial} {
		got, ok := decoded[index]["partial"]
		if !ok || got != want {
			t.Fatalf("result %d partial=%v (present=%t), want %q: %s", index, got, ok, want, encoded)
		}
		if decoded[index]["method"] != "browser-chrome" {
			t.Fatalf("result %d method=%v, want browser-chrome: %s", index, decoded[index]["method"], encoded)
		}
	}
}
