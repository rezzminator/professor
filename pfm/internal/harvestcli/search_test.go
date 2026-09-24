package harvestcli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

// fakeDiscovery routes every search_literature source to a fixture: OpenAlex
// answers one paper, every other source answers an empty object. It returns a
// pointer to the number of services built, so a refused flag proves no search
// ran.
func fakeDiscovery(t *testing.T) *int {
	t.Helper()
	built := 0
	client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		if r.URL.Host == "api.openalex.org" {
			body = `{"results":[{"doi":"https://doi.org/10.1038/nature14539","display_name":"Deep learning","publication_year":2015}]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	previous := newSearchService
	newSearchService = func(runtime harvestmcp.Runtime) (*harvestmcp.Service, error) {
		built++
		runtime.Client = client
		return harvestmcp.NewConfiguredHarvester("test", runtime)
	}
	t.Cleanup(func() { newSearchService = previous })
	return &built
}

func harvestSearch(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Harvest(append([]string{"search"}, args...), &stdout, &stderr, downloadRuntime(t))
	return code, stdout.String(), stderr.String()
}

// TestHarvestSearchPrintsCandidatesWithTypeAndSources: the text answer names
// each candidate's title, type and handle, then every source's status.
func TestHarvestSearchPrintsCandidatesWithTypeAndSources(t *testing.T) {
	fakeDiscovery(t)
	code, stdout, stderr := harvestSearch(t, "Deep", "learning")
	if code != 0 {
		t.Fatalf("search code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{`for "Deep learning"`, "1. Deep learning", "type: ", "handle: ", "sources:\n", ": answered, "} {
		if !strings.Contains(stdout, want) {
			t.Errorf("search output omits %q:\n%s", want, stdout)
		}
	}
}

// TestHarvestSearchJSONCarriesTypedCandidates: --json prints the tool's
// structured result — candidates with their type (never kind) and sources.
func TestHarvestSearchJSONCarriesTypedCandidates(t *testing.T) {
	fakeDiscovery(t)
	code, stdout, stderr := harvestSearch(t, "--type", "paper", "--limit", "3", "--json", "Deep learning")
	if code != 0 {
		t.Fatalf("search --json code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var found struct {
		Candidates []map[string]any `json:"candidates"`
		Sources    []map[string]any `json:"sources"`
	}
	if err := json.Unmarshal([]byte(stdout), &found); err != nil {
		t.Fatalf("search --json is not the structured result: %v\n%s", err, stdout)
	}
	if len(found.Candidates) == 0 || len(found.Candidates) > 3 || len(found.Sources) == 0 {
		t.Fatalf("search --json = %s, want 1 to 3 candidates and the sources", stdout)
	}
	for _, candidate := range found.Candidates {
		if _, stale := candidate["kind"]; stale || candidate["type"] == nil || candidate["type"] == "" ||
			candidate["handle"] == nil {
			t.Fatalf("candidate = %v, want its type under type and a handle", candidate)
		}
	}
}

// TestHarvestSearchRefusesBadFlagsByName: a bad --type or --limit, or no
// query, is a usage error naming what is wrong, and no search runs.
func TestHarvestSearchRefusesBadFlagsByName(t *testing.T) {
	built := fakeDiscovery(t)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--type", "movie", "Deep learning"}, `--type must be any, paper, book, got "movie"`},
		{[]string{"--limit", "0", "Deep learning"}, "--limit must be between 1 and 25, got 0"},
		{[]string{"--limit", "26", "Deep learning"}, "--limit must be between 1 and 25, got 26"},
		{[]string{"--limit", "many", "Deep learning"}, "limit"},
		{[]string{"--type", "paper"}, "usage: pfm harvest search"},
	} {
		code, stdout, stderr := harvestSearch(t, test.args...)
		if code != 2 || !strings.Contains(stderr, test.want) || stdout != "" {
			t.Errorf(
				"search %v: code=%d stdout=%q stderr=%q, want 2 naming %q",
				test.args,
				code,
				stdout,
				stderr,
				test.want,
			)
		}
	}
	if *built != 0 {
		t.Fatalf("%d searches ran for refused flags, want none", *built)
	}
}
