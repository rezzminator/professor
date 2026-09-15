package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestFindWorksIncludesConfiguredProvidersAndPublicHandleFetchesSelectedPDF(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	md5 := "9de4a86150a39b54d3e01f98678468bf"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "ipfs-catalog.test":
			return response(r, http.StatusOK, "text/html", `<a href="/md5/`+md5+`">Anna result</a>`), nil
		case "md5-catalog.test":
			return response(
				r,
				http.StatusOK,
				"text/html",
				`<table><tr><td><a href="/edition.php?id=4">MD5Catalog result</a></td><td>Author</td><td>Publisher</td><td>2020</td><td><a href="/ads.php?md5=`+md5+`&key=x">GET</a></td></tr></table>`,
			), nil
		case "scholar.test":
			if r.URL.Path == "/selected.pdf" {
				return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nselected\n%%EOF"), nil
			}
			return response(
				r,
				http.StatusOK,
				"text/html",
				`<div class="gs_ri"><h3 class="gs_rt"><a href="https://doi.org/10.1234/provider.fixture">Scholar result</a></h3><div class="gs_a">A Author - Journal, 2020 - repository.example</div><div class="gs_or_ggsm"><a href="https://scholar.test/selected.pdf">[PDF]</a></div></div>`,
			), nil
		default:
			return response(r, http.StatusOK, "application/json", `{}`), nil
		}
	})}
	resolver := &Resolver{
		Client:           client,
		IPFSCatalogURL:   "https://ipfs-catalog.test",
		MD5CatalogURL:    "https://md5-catalog.test",
		GoogleScholarURL: "https://scholar.test",
	}
	candidates, err := resolver.FindWorks(context.Background(), "fixture query", 10)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	var selected Candidate
	for _, candidate := range candidates {
		sources[candidate.Source] = true
		if candidate.Source == "google-scholar" {
			selected = candidate
		}
	}
	for _, source := range []string{"ipfs-catalog", "md5-catalog", "google-scholar"} {
		if !sources[source] {
			t.Fatalf("FindWorks candidates omitted configured provider %q: %#v", source, candidates)
		}
	}
	if selected.URL != "https://scholar.test/selected.pdf" {
		t.Fatalf("selected Scholar URL = %q, want exact PDF location", selected.URL)
	}

	h := mustNew(t, Options{
		CacheDir: t.TempDir(), Client: client,
		Converter: legacyConverterFunc(func(_ context.Context, _, _ string, _ []byte) (string, error) {
			return "selected provider bytes", nil
		}),
	})
	public, err := h.PublicCandidates([]Candidate{selected})
	if err != nil || len(public) != 1 || !publicHandleRE.MatchString(public[0].URL) {
		t.Fatalf("PublicCandidates(selected) = %#v err=%v; want opaque handle", public, err)
	}
	got := h.FetchPublic(context.Background(), public[0].URL, FetchOptions{})
	if got.Error != "" || got.Content != "selected provider bytes" || strings.Contains(got.Content, "scholar.test") {
		t.Fatalf("selected public handle fetch = %#v", got)
	}
}
