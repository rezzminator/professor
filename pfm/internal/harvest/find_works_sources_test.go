package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestFindWorksNamesAFailedSource: Semantic Scholar answers HTTP 429 while
// OpenAlex answers; the candidates stay, Semantic Scholar is named failed
// with its status, and a source that answered with nothing is named answered.
func TestFindWorksNamesAFailedSource(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.semanticscholar.org":
			return response(r, http.StatusTooManyRequests, "application/json", `{"message":"Too Many Requests"}`), nil
		case "api.openalex.org":
			return response(r, http.StatusOK, "application/json", `{"results":[
{"doi":"https://doi.org/10.1038/nature14539","display_name":"Deep learning","publication_year":2015}]}`), nil
		}
		return response(r, http.StatusOK, "application/json", `{}`), nil
	})}
	found, err := (&Resolver{Client: client}).FindWorksReport(context.Background(), "Deep learning", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Candidates) == 0 {
		t.Fatalf("candidates = none, want OpenAlex's record kept")
	}
	status := map[string]WorkSource{}
	for _, source := range found.Sources {
		status[source.Name] = source
	}
	if got := status["Semantic Scholar"]; got.Status != SourceFailed || !strings.Contains(got.Error, "HTTP 429") {
		t.Fatalf("Semantic Scholar = %+v, want failed with HTTP 429 named; sources %+v", got, found.Sources)
	}
	if got := status["OpenAlex"]; got.Status != SourceAnswered || got.Results != 1 {
		t.Fatalf("OpenAlex = %+v, want answered with 1 result", got)
	}
	if got := status["Crossref"]; got.Status != SourceAnswered || got.Results != 0 {
		t.Fatalf("Crossref = %+v, want answered with no result — distinct from failed", got)
	}
}

// TestFindWorksEverySourceFailedIsAnError: no source answered, so the answer
// is an error naming each source, never an empty list.
func TestFindWorksEverySourceFailedIsAnError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusServiceUnavailable, "application/json", `{}`), nil
	})}
	candidates, err := (&Resolver{Client: client}).FindWorks(context.Background(), "Deep learning", 8)
	if err == nil {
		t.Fatalf("FindWorks = %+v, nil error; want every failed source named", candidates)
	}
	for _, name := range []string{"OpenAlex", "arXiv", "Crossref", "Semantic Scholar", "Open Library and Gutendex", "HTTP 503"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}
