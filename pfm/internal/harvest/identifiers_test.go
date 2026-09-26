package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestIdentifiersAndOAOrdering(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"doi:10.1000/ABC.", "10.1000/ABC"},
		{"https://doi.org/10.1000/abc", "10.1000/abc"},
		{"978-0-306-40615-7", "9780306406157"},
		{"0-14-044913-2", "0140449132"},
	} {
		if got := NormalizeIdentifier(tc.in); got != tc.want {
			t.Errorf("NormalizeIdentifier(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := NormalizeIdentifier("9780306406158"); got != "" {
		t.Fatalf("bad ISBN accepted as %q", got)
	}
	if got := ClassifyIdentifier("PMC3786668"); got != IdentifierPMCID {
		t.Fatalf("PMCID class = %q", got)
	}
	// An arXiv id is read as its arXiv DOI, so the scholarly path fetches the
	// paper instead of calling it a title.
	for _, tc := range []struct{ in, want string }{
		{"arXiv:1706.03762", "10.48550/arXiv.1706.03762"},
		{"ARXIV: 1706.03762v5", "10.48550/arXiv.1706.03762v5"},
		{"1706.03762", "10.48550/arXiv.1706.03762"},
		{"arXiv:hep-th/9901001", "10.48550/arXiv.hep-th/9901001"},
		{"math.AG/0309136", "10.48550/arXiv.math.AG/0309136"},
	} {
		if got := ClassifyIdentifier(tc.in); got != IdentifierDOI {
			t.Errorf("ClassifyIdentifier(%q) = %q, want doi", tc.in, got)
		}
		if got := NormalizeIdentifier(tc.in); got != tc.want {
			t.Errorf("NormalizeIdentifier(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// An arxiv.org URL stays a web page here (urls reads the page); only
	// publications turns it into its id.
	for _, notID := range []string{"https://arxiv.org/abs/1706.03762", "1706.037", "arXiv 1706.03762 notes"} {
		if got := ClassifyIdentifier(notID); got != IdentifierNone {
			t.Errorf("ClassifyIdentifier(%q) = %q, want none", notID, got)
		}
	}

	// Unpaywall is gated on an operator email (it 422s keyless) — the test opts in.
	resolver := &Resolver{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		switch {
		case strings.Contains(u, "api.unpaywall.org"):
			return jsonResponse(
				r,
				`{"is_oa":true,"oa_status":"gold","best_oa_location":{"url_for_pdf":"https://repo.test/paper.pdf","version":"publishedVersion"}}`,
			), nil
		case strings.Contains(u, "openalex.org"):
			return jsonResponse(r, `{"open_access":{"is_oa":true,"oa_status":"green"},"locations":[]}`), nil
		default:
			return response(r, http.StatusNotFound, "application/json", `{}`), nil
		}
	})}}
	resolver.ContactEmail = "test@example.org"
	cands, err := resolver.ResolveDOI(context.Background(), "10.1000/test")
	if err != nil || len(cands) == 0 || cands[0].URL != "https://repo.test/paper.pdf" {
		t.Fatalf("ResolveDOI() = %#v err=%v", cands, err)
	}
}
