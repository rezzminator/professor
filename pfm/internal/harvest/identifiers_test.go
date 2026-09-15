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
