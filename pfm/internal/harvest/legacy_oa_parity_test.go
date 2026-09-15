package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestLegacyOAMixedCaseArxivDOIShortCircuitsExactly(t *testing.T) {
	resolver := &Resolver{
		Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			t.Fatalf("mixed-case arXiv DOI unexpectedly used network: %s", request.URL)
			return nil, nil
		})},
	}
	candidates, err := resolver.ResolveDOI(context.Background(), "10.48550/arXiv.1706.03762")
	// Since the wave: the direct PDF is joined by the ar5iv HTML insurance copy
	// (parity with harvester oa.arxiv_candidates).
	if err != nil || len(candidates) != 2 || candidates[0].URL != "https://arxiv.org/pdf/1706.03762" ||
		candidates[1].URL != "https://ar5iv.labs.arxiv.org/html/1706.03762" {
		t.Fatalf("mixed-case arXiv candidates=%#v err=%v", candidates, err)
	}
}

func TestLegacyResolveTitleExpandsConfidentDOIThroughOAChain(t *testing.T) {
	client := legacyOAClient(t, func(request *http.Request) string {
		u := request.URL.String()
		switch {
		case strings.Contains(u, "api.openalex.org/works?filter=title.search"):
			return `{"results":[{"display_name":"Array programming with NumPy","doi":"https://doi.org/10.1038/s41586-020-2649-2"}]}`
		case strings.Contains(u, "api.unpaywall.org"):
			return `{"is_oa":true,"oa_status":"gold","best_oa_location":{"url_for_pdf":"https://public.example.test/numpy.pdf","version":"publishedVersion"}}`
		case strings.Contains(u, "pmc.ncbi.nlm.nih.gov/tools/idconv"):
			return `{"records":[]}`
		case strings.Contains(u, "api.openalex.org/works/https://doi.org"):
			return `{}`
		case strings.Contains(u, "api.semanticscholar.org"):
			return `{}`
		case strings.Contains(u, "api.crossref.org/works/"):
			return `{"message":{"link":[]}}`
		case strings.Contains(u, "api.core.ac.uk"):
			return `{}`
		case strings.Contains(u, "doaj.org"):
			return `{"results":[]}`
		default:
			return `{}`
		}
	})
	resolver := &Resolver{Client: client, ContactEmail: "test@example.org"}
	candidates, err := resolver.ResolveTitle(context.Background(), "Array programming with NumPy")
	if err != nil || len(candidates) == 0 || candidates[0].URL != "https://public.example.test/numpy.pdf" {
		t.Fatalf("title OA expansion=%#v err=%v", candidates, err)
	}
}

func TestLegacyFindWorksMergesPaperArxivAndBookDiscovery(t *testing.T) {
	client := legacyOAClient(t, func(request *http.Request) string {
		u := request.URL.String()
		switch {
		case strings.Contains(u, "api.openalex.org/works?filter=title.search"):
			return `{"results":[{"display_name":"A Mathematical Theory of Communication","doi":"https://doi.org/10.1002/exact","publication_year":1948,"open_access":{"oa_status":"closed"},"authorships":[{"author":{"display_name":"C. E. Shannon"}}]},{"display_name":"Something else entirely","doi":"https://doi.org/10.1002/other","open_access":{},"authorships":[]}]}`
		case strings.Contains(u, "api.crossref.org/works?query.bibliographic"):
			return `{"message":{"items":[]}}`
		case strings.Contains(u, "export.arxiv.org"):
			return `<feed><entry><title>A Mathematical Theory of Communication</title><id>http://arxiv.org/abs/2401.00001v1</id></entry></feed>`
		case strings.Contains(u, "openlibrary.org/search.json"):
			return `{"docs":[{"title":"A Mathematical Theory of Communication","author_name":["Fixture Author"],"first_publish_year":1950,"isbn":["9780000000000"],"ebook_access":"public"}]}`
		case strings.Contains(u, "gutendex.com"):
			return `{"results":[{"title":"A Mathematical Theory of Communication","copyright":false,"authors":[{"name":"Fixture Author"}],"formats":{"text/plain; charset=utf-8":"https://public.example.test/book.txt","text/html":"https://public.example.test/book.html"}}]}`
		case strings.Contains(u, "library.oapen.org/rest/search"):
			return `[]`
		case strings.Contains(u, "directory.doabooks.org/rest/search"):
			return `[]`
		default:
			return `{}`
		}
	})
	got, err := (&Resolver{Client: client}).FindWorks(context.Background(), "A Mathematical Theory of Communication", 8)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"10.1002/exact":                         false,
		"https://arxiv.org/pdf/2401.00001v1":    false,
		"isbn:9780000000000":                    false,
		"https://public.example.test/book.html": false,
	}
	for _, candidate := range got {
		if _, ok := want[candidate.URL]; ok {
			want[candidate.URL] = true
		}
	}
	for handle, found := range want {
		if !found {
			t.Errorf("findWorks missing %q: %#v", handle, got)
		}
	}
	var paper Candidate
	for _, candidate := range got {
		if candidate.URL == "10.1002/exact" {
			paper = candidate
		}
	}
	if paper.Authors != "C. E. Shannon" || paper.Year != 1948 || paper.Kind != "paper" {
		t.Fatalf("findWorks paper metadata=%#v", got)
	}
	if len(got) == 0 || strings.EqualFold(got[0].Free, "closed") {
		t.Fatalf("free exact-match candidates did not outrank the closed copy: %#v", got)
	}
}

func legacyOAClient(t *testing.T, body func(*http.Request) string) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, body(request)), nil
	})}
}
