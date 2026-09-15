package harvest

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/paths"
)

func TestLegacyTokenEstimatorAllTenBehaviors(t *testing.T) {
	code := strings.Repeat("a=[1];b={k:v};c=(x)+y*z/w;", 40)
	cjkSymbols := strings.Repeat("中{}[]", 50)
	tests := []struct {
		name string
		body string
		want int
	}{
		{"empty is zero", "", 0},
		{"Latin prose uses half", strings.Repeat("word ", 200), 500},
		{"prose rounds up", "abc", 2},
		{"code density uses 1.8 divisor", code, int(math.Ceil(float64(len([]rune(code))) / 1.8))},
		{"CJK uses 1.3 multiplier", strings.Repeat("中文测试", 25), 130},
		{"Korean is East Asian", strings.Repeat("가나다라", 25), 130},
		{"CJK wins over symbol density", cjkSymbols, int(math.Ceil(float64(len([]rune(cjkSymbols))) * 1.3))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := EstimateTokens(test.body); got != test.want {
				t.Fatalf("EstimateTokens()=%d, want %d", got, test.want)
			}
		})
	}
	if EstimateTokens(strings.Repeat("中", 100)) <= EstimateTokens(strings.Repeat("a ", 50)) {
		t.Fatal("CJK estimate must exceed prose at the same rune count")
	}
	for _, body := range []string{strings.Repeat("The quick brown fox. ", 100), strings.Repeat("Lorem ipsum dolor sit amet ", 50)} {
		if EstimateTokens(body) < len([]rune(body))/4 {
			t.Fatalf("estimate %d under-counts lower bound for %d runes", EstimateTokens(body), len([]rune(body)))
		}
	}
	if EstimateTokens(strings.Repeat("word ", 200)) < EstimateTokens(strings.Repeat("word ", 100)) {
		t.Fatal("token estimate is not monotonic")
	}
}

// TestCacheRootIsConfiguredDirOrTheOneDefault pins the single cache-root
// rule: the configured dir (harvester.config.json cache.dir) when set, else
// <home>/.professor/.cache — never the working directory, and never the
// retired WEBFETCH_DIR / HARVESTER_CACHE_DIR variables.
func TestCacheRootIsConfiguredDirOrTheOneDefault(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("WEBFETCH_DIR", filepath.Join(root, "legacy"))
	t.Setenv("HARVESTER_CACHE_DIR", ".harvest-cache")
	jailHome := filepath.Join(root, "home")
	t.Setenv(paths.EnvHome, jailHome)
	if got, err := CacheRoot(""); err != nil || got != filepath.Join(jailHome, ".professor", ".cache") {
		t.Fatalf("default cache root=%q err=%v; the retired env variables must be ignored", got, err)
	}
	t.Setenv(paths.EnvHome, "")
	if got, err := CacheRoot(""); err == nil {
		t.Fatalf("unjailed default cache root resolved to %q; want the real-home refusal", got)
	}
	abs := filepath.Join(root, "elsewhere")
	if got, err := CacheRoot(abs); err != nil || got != abs {
		t.Fatalf("configured cache root=%q err=%v", got, err)
	}
}

func TestResolveTTLDefaultsZeroAndHonorsExplicitZero(t *testing.T) {
	if got := resolveTTL(0, time.Hour); got != time.Hour {
		t.Fatalf("zero TTL = %s, want the default", got)
	}
	if got := resolveTTL(-1, time.Hour); got != 0 {
		t.Fatalf("negative TTL = %s, want explicit zero", got)
	}
	if got := resolveTTL(time.Minute, time.Hour); got != time.Minute {
		t.Fatalf("configured TTL = %s", got)
	}
}

func TestLegacyCacheFreshnessAllKindsAndFrontmatterWins(t *testing.T) {
	root := t.TempDir()
	cache := newCache(root, 24*time.Hour)
	now := time.Now().UTC()
	write := func(name, fetchedAt string, mtime time.Time) string {
		t.Helper()
		path := filepath.Join(root, name+".md")
		raw := "---\nfetched_at: " + fetchedAt + "\n---\n\nbody"
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}
	oldStamp := now.Add(-25 * time.Hour).Format(time.RFC3339)
	freshStamp := now.Add(-23 * time.Hour).Format(time.RFC3339)
	for kind := range volatileKinds {
		old := write("old-"+kind, oldStamp, now)
		if !cache.stale(old, kind, map[string]string{"fetched_at": oldStamp}) {
			t.Errorf("old %s artifact reported fresh", kind)
		}
		fresh := write("fresh-"+kind, freshStamp, now.Add(-365*24*time.Hour))
		if cache.stale(fresh, kind, map[string]string{"fetched_at": freshStamp}) {
			t.Errorf("fresh %s artifact reported stale", kind)
		}
	}
	for _, kind := range []string{"png", "jpg", "image", "archive_member", "zip"} {
		path := write("static-"+kind, oldStamp, now.Add(-365*24*time.Hour))
		if cache.stale(path, kind, map[string]string{"fetched_at": oldStamp}) {
			t.Errorf("static %s artifact expired", kind)
		}
	}
	frontmatterOld := write("frontmatter-wins", now.Add(-48*time.Hour).Format(time.RFC3339), now)
	if !cache.stale(
		frontmatterOld,
		"html",
		map[string]string{"fetched_at": now.Add(-48 * time.Hour).Format(time.RFC3339)},
	) {
		t.Fatal("fresh mtime incorrectly overrode old fetched_at")
	}
	if cache.stale(filepath.Join(root, "unstatable"), "html", map[string]string{"fetched_at": "broken"}) {
		t.Fatal("unreadable timestamp/stat must degrade to serving")
	}
	if newCache(root, 0).stale(frontmatterOld, "html", map[string]string{"fetched_at": oldStamp}) {
		t.Fatal("zero TTL did not disable expiry")
	}
}

func TestLegacyRefreshBypassesNegativeCache(t *testing.T) {
	var calls int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("fixture connection failure")
	})
	client := func() *http.Client { return &http.Client{Transport: transport} }
	h := mustNew(
		t,
		Options{
			CacheDir:  t.TempDir(),
			Client:    client(),
			Chrome:    client(),
			Jina:      client(),
			OA:        client(),
			Converter: &fakeConverter{},
		},
	)
	source := "https://failure.example.test/page"
	first := h.Fetch(context.Background(), source)
	if first.Error == "" {
		t.Fatal("first failure unexpectedly succeeded")
	}
	firstCalls := calls
	second := h.Fetch(context.Background(), source)
	if !strings.Contains(second.Error, first.Error) || !strings.Contains(second.Error, "recently failed; cached") ||
		calls != firstCalls {
		t.Fatalf("negative cache miss: second=%#v calls=%d want %d", second, calls, firstCalls)
	}
	third := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
	if third.Error == "" || calls <= firstCalls {
		t.Fatalf("refresh did not bypass negative cache: third=%#v calls=%d", third, calls)
	}
}

func TestLegacyDOIISBNMetaAndMirrorPureCases(t *testing.T) {
	doiCases := map[string]string{
		"10.1073/pnas.2302738120":                                      "10.1073/pnas.2302738120",
		"https://pnas.org/doi/10.1073/pnas.2302738120":                 "10.1073/pnas.2302738120",
		"See doi 10.1038/s41586-023-05881-4 for details.":              "10.1038/s41586-023-05881-4",
		"10.1073/pnas.2302738120.":                                     "10.1073/pnas.2302738120",
		"10.1073/pnas.2302738120,":                                     "10.1073/pnas.2302738120",
		"10.1073/pnas.2302738120;":                                     "10.1073/pnas.2302738120",
		"(10.1073/pnas.2302738120)":                                    "10.1073/pnas.2302738120",
		"10.1073/pnas.2302738120>":                                     "10.1073/pnas.2302738120",
		"10.1016/J.CELL.2023.01.001":                                   "10.1016/J.CELL.2023.01.001",
		"First 10.1073/pnas.111 and second 10.1038/s41586-023-05881-4": "10.1073/pnas.111",
		"10.1186/s12859-023-05301-w":                                   "10.1186/s12859-023-05301-w",
		"hello world":                                                  "",
		"https://example.test/page":                                    "",
		"":                                                             "",
		"version 10.2 of the spec":                                     "",
	}
	for input, want := range doiCases {
		if got := DOIFrom(input); got != want {
			t.Errorf("DOIFrom(%q)=%q, want %q", input, got, want)
		}
	}
	isbnCases := map[string]string{
		"978-0-262-30098-8": "9780262300988",
		"0-201-61622-X":     "020161622X",
		"Frankenstein":      "",
		"12345":             "",
		"978-0-262-30098-7": "",
		"0000000000":        "",
		"9781111111111":     "",
	}
	for input, want := range isbnCases {
		if got := NormalizeISBN(input); got != want {
			t.Errorf("NormalizeISBN(%q)=%q, want %q", input, got, want)
		}
	}
	page := `<meta name="citation_pdf_url" content="https://public.example.test/x.pdf"><meta name="citation_doi" content="10.1234/abc"><meta name="citation_title" content="A Great Paper">`
	doi, pdf, title := ExtractMetaLinks(page)
	if doi != "10.1234/abc" || pdf != "https://public.example.test/x.pdf" || title != "A Great Paper" {
		t.Fatalf("ExtractMetaLinks()=(%q,%q,%q)", doi, pdf, title)
	}
	_, reversed, _ := ExtractMetaLinks(`<meta content="https://public.example.test/y.pdf" name="citation_pdf_url"/>`)
	if reversed != "https://public.example.test/y.pdf" {
		t.Fatalf("reversed citation_pdf_url=%q", reversed)
	}
	if got := PMCArticleURL("PMC1"); got != "https://pmc.ncbi.nlm.nih.gov/articles/PMC1/" {
		t.Fatalf("PMC URL=%q", got)
	}
	if got := EuropePMCFiguresURL(
		"PMC42",
	); got != "https://www.ebi.ac.uk/europepmc/webservices/rest/PMC42/supplementaryFiles" {
		t.Fatalf("Europe PMC figures URL=%q", got)
	}
}

func TestLegacyWaybackAndPMCIDResponses(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "archive.org":
			return jsonResponse(
				request,
				`{"archived_snapshots":{"closest":{"available":true,"timestamp":"20230601123456"}}}`,
			), nil
		case "pmc.ncbi.nlm.nih.gov":
			return jsonResponse(request, `{"records":[{"pmcid":"PMC10450651"}]}`), nil
		default:
			return response(request, http.StatusNotFound, "application/json", `{}`), nil
		}
	})}
	wayback, err := WaybackRawURL(context.Background(), client, "https://public.example.test/article")
	if err != nil || wayback != "https://web.archive.org/web/20230601123456id_/https://public.example.test/article" {
		t.Fatalf("WaybackRawURL()=%q err=%v", wayback, err)
	}
	pmcid, err := DOIToPMCID(context.Background(), client, "10.1234/example")
	if err != nil || pmcid != "PMC10450651" {
		t.Fatalf("DOIToPMCID()=%q err=%v", pmcid, err)
	}
}

func TestSearchEnabledNeedsABackendAndNotDisabled(t *testing.T) {
	t.Setenv("SEARXNG_URL", "https://env.example.test")
	t.Setenv("BRAVE_API_KEY", "env-key")
	if SearchEnabled(SearchOptions{}) {
		t.Fatal("search enabled without a configured backend (retired env variables must be ignored)")
	}
	if !SearchEnabled(SearchOptions{SearXNGURL: "https://search.example.test"}) {
		t.Fatal("SearXNG did not enable search")
	}
	if !SearchEnabled(SearchOptions{BraveAPIKey: "example-fixture-key"}) {
		t.Fatal("Brave did not enable search")
	}
	if SearchEnabled(
		SearchOptions{
			SearXNGURL:    "https://search.example.test",
			BraveAPIKey:   "example-fixture-key",
			DisableSearch: true,
		},
	) {
		t.Fatal("disabled search reported enabled")
	}
}
