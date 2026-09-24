package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
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
	for _, name := range []string{"OpenAlex", "arXiv", "Crossref", "Semantic Scholar", "Open Library", "Gutendex", "HTTP 503"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

// TestFindWorksAnswersWithinItsDeadline: Semantic Scholar sleeps past the
// deadline while OpenAlex and Crossref answer at once. The call returns
// within the deadline plus a small margin, ranks the fast sources' records
// and names Semantic Scholar timed_out with the time it was given — a source
// still running is never silently dropped.
func TestFindWorksAnswersWithinItsDeadline(t *testing.T) {
	const deadline = 300 * time.Millisecond
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.semanticscholar.org":
			time.Sleep(3 * time.Second) // past the deadline, deaf to cancellation
			return response(r, http.StatusOK, "application/json", `{"data":[]}`), nil
		case "api.openalex.org":
			return response(r, http.StatusOK, "application/json", `{"results":[
{"doi":"https://doi.org/10.1038/nature14539","display_name":"Deep learning","publication_year":2015}]}`), nil
		case "api.crossref.org":
			return response(r, http.StatusOK, "application/json", `{"message":{"items":[
{"DOI":"10.1000/deep","title":["Deep learning"],"issued":{"date-parts":[[2016]]}}]}}`), nil
		}
		return response(r, http.StatusOK, "application/json", `{}`), nil
	})}
	start := time.Now()
	found, err := (&Resolver{Client: client}).findWorksWithin(
		context.Background(),
		"Deep learning",
		8,
		"",
		deadline,
		findWorksGrace,
	)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > deadline+500*time.Millisecond {
		t.Fatalf("findWorks took %s, want within the %s deadline plus a small margin", elapsed, deadline)
	}
	if len(found.Candidates) != 2 || found.Candidates[0].Year != 2015 {
		t.Fatalf("candidates = %+v, want OpenAlex's and Crossref's records ranked, the earlier first", found.Candidates)
	}
	status := map[string]WorkSource{}
	for _, source := range found.Sources {
		status[source.Name] = source
	}
	got := status["Semantic Scholar"]
	if got.Status != SourceTimedOut || !strings.Contains(got.Error, deadline.String()) {
		t.Fatalf(
			"Semantic Scholar = %+v, want timed_out naming the %s it was given; sources %+v",
			got,
			deadline,
			found.Sources,
		)
	}
	if got := status["OpenAlex"]; got.Status != SourceAnswered || got.Results != 1 {
		t.Fatalf("OpenAlex = %+v, want answered with 1 result", got)
	}
}

// slowGutendexClient answers every paper source and Open Library at once with
// one record each; Gutendex sleeps past any deadline, deaf to cancellation.
// slowHost, when set, sleeps the same way.
func slowGutendexClient(slowHost string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "gutendex.com" || r.URL.Host == slowHost {
			time.Sleep(5 * time.Second)
			return response(r, http.StatusOK, "application/json", `{}`), nil
		}
		switch r.URL.Host {
		case "api.openalex.org":
			return response(r, http.StatusOK, "application/json", `{"results":[
{"doi":"https://doi.org/10.1038/nature14539","display_name":"Deep learning","publication_year":2015}]}`), nil
		case "api.crossref.org":
			return response(r, http.StatusOK, "application/json", `{"message":{"items":[
{"DOI":"10.1000/deep","title":["Deep learning"],"issued":{"date-parts":[[2016]]}}]}}`), nil
		}
		return response(r, http.StatusOK, "application/json", `{}`), nil
	})}
}

// TestFindWorksFinishesEarlyOnceTheCoreAnswered: the core answered at once and
// only Gutendex runs long. The call returns within the core's time plus the
// grace, not at the 20 s deadline, and names Gutendex timed_out with the
// early-finish text, apart from the deadline's.
func TestFindWorksFinishesEarlyOnceTheCoreAnswered(t *testing.T) {
	start := time.Now()
	found, err := (&Resolver{Client: slowGutendexClient("")}).FindWorksReportFor(
		context.Background(), "Deep learning", 1, "")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > findWorksGrace+500*time.Millisecond {
		t.Fatalf("findWorks took %s, want the core's time plus the %s grace", elapsed, findWorksGrace)
	}
	if got := sourceNamed(found, "Gutendex"); got.Status != SourceTimedOut || got.Error != findWorksNotWaitedText {
		t.Fatalf("Gutendex = %+v, want timed_out %q", got, findWorksNotWaitedText)
	}
}

// TestFindWorksEarlyFinishFollowsTheKind: the core is the asked-for kind's
// sources. A slow source outside it is not waited for past the grace; a slow
// core source, or an "any" call short of limit candidates, holds the call to
// the deadline.
func TestFindWorksEarlyFinishFollowsTheKind(t *testing.T) {
	const deadline, grace = 1500 * time.Millisecond, 200 * time.Millisecond
	for _, tc := range []struct {
		name, kind, slowHost, slowSource, wantError string
		limit                                       int
		early                                       bool
	}{
		{"paper, slow Open Library", kindPaper, "openlibrary.org", "Open Library", findWorksNotWaitedText, 8, true},
		{"paper, slow Crossref", kindPaper, "api.crossref.org", "Crossref", "deadline", 8, false},
		{"book, slow Gutendex", kindBook, "", "Gutendex", "deadline", 8, false},
		{"any, fewer than limit", "", "", "Gutendex", "deadline", 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			found, err := (&Resolver{Client: slowGutendexClient(tc.slowHost)}).findWorksWithin(
				context.Background(), "Deep learning", tc.limit, tc.kind, deadline, grace)
			elapsed := time.Since(start)
			if err != nil && tc.early {
				t.Fatal(err)
			}
			if tc.early && elapsed > grace+500*time.Millisecond {
				t.Fatalf("took %s, want the core's time plus the %s grace", elapsed, grace)
			}
			if !tc.early && elapsed < deadline {
				t.Fatalf("took %s, want the call held to the %s deadline", elapsed, deadline)
			}
			got := sourceNamed(found, tc.slowSource)
			if got.Status != SourceTimedOut || !strings.Contains(got.Error, tc.wantError) {
				t.Fatalf(
					"%s = %+v, want timed_out naming %q; sources %+v",
					tc.slowSource,
					got,
					tc.wantError,
					found.Sources,
				)
			}
		})
	}
}

func sourceNamed(found WorksFound, name string) WorkSource {
	for _, source := range found.Sources {
		if source.Name == name {
			return source
		}
	}
	return WorkSource{}
}
