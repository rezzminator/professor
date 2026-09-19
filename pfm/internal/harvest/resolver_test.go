package harvest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestLegacyUnknownOASourceKeepsWorstDefaultPriority(t *testing.T) {
	if got := candidatePriority("future-provider", "", "", "pdf"); got != 50 {
		t.Fatalf("unknown provider priority=%d, want legacy default 50", got)
	}
}

// TestProviderContactComesFromOptionsNotEnvironment pins the config-only
// contract: the scholarly identity is Options.ContactEmail (fed from
// harvester.config.json), and the retired HARVESTER_CONTACT_EMAIL is ignored.
func TestProviderContactComesFromOptionsNotEnvironment(t *testing.T) {
	t.Setenv("HARVESTER_CONTACT_EMAIL", "env@example.test")
	var seen *http.Request
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.Clone(r.Context())
		return jsonResponse(r, `{"records":[{"pmcid":"PMC1234567"}]}`), nil
	})}
	h := mustNew(t, Options{CacheDir: t.TempDir(), OA: client, ContactEmail: "config@example.test"})
	_, err := h.resolver().ResolvePMID(context.Background(), "1234567")
	if err != nil {
		t.Fatal(err)
	}
	if seen == nil {
		t.Fatal("resolver made no idconv request")
	}
	if got := seen.URL.Query().Get("email"); got != "config@example.test" {
		t.Fatalf("contact query = %q, want config@example.test", got)
	}
	if got := seen.Header.Get("User-Agent"); got != "harvester-mcp/1.0 (mailto:config@example.test)" {
		t.Fatalf("scholarly UA = %q", got)
	}
}

// TestGetJSONWithHeadersRefusesOversizeBodyByName: getJSONWithHeaders
// previously ran through getBodyWithHeaders (oversizeTruncate: true), so an
// over-ceiling JSON response was silently truncated and then failed
// json.Unmarshal with "unexpected end of JSON input" — an error that reads
// as a malformed response from the source when the real story is the byte
// ceiling. The error must name the ceiling, not describe a decode failure.
func TestGetJSONWithHeadersRefusesOversizeBodyByName(t *testing.T) {
	withPublicDNSForProviderTest(t)
	oversize := strings.Repeat("a", resolverJSONMaxBody+1<<20)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "application/json", `{"x":"`+oversize+`"}`), nil
	})}
	var dst any
	err := getJSONWithHeaders(context.Background(), client, "https://api.example.test/big", nil, &dst)
	if err == nil {
		t.Fatal("getJSONWithHeaders error = nil, want an oversize refusal")
	}
	if strings.Contains(err.Error(), "unexpected end of JSON input") || strings.Contains(err.Error(), "decode JSON") {
		t.Fatalf("getJSONWithHeaders error = %q, want it to name the byte ceiling rather than a decode failure", err)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("getJSONWithHeaders error = %q, want it to name the byte ceiling", err)
	}
}

// TestResolveTitleReportsOutageWhenBothProvidersFail pins F13: OpenAlex and
// Crossref both failing to answer at all used to read as an ordinary
// no-title-match (titleToDOI discarded both errors with `_ =` /
// `crossErr == nil` with no else). Watched FAILING before the fix (err was
// nil).
func TestResolveTitleReportsOutageWhenBothProvidersFail(t *testing.T) {
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Host, "openalex.org"):
			return nil, errors.New("openalex unreachable")
		case strings.Contains(r.URL.Host, "crossref.org"):
			return nil, errors.New("crossref unreachable")
		case strings.Contains(r.URL.Host, "arxiv.org"):
			return response(r, http.StatusOK, "application/atom+xml", "<feed></feed>"), nil
		default:
			t.Fatalf("unexpected host %s", r.URL.Host)
			return nil, nil
		}
	})}
	resolver := &Resolver{Client: client}
	candidates, err := resolver.ResolveTitle(context.Background(), "A Title Nobody Indexed")
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want none", candidates)
	}
	if err == nil {
		t.Fatal("ResolveTitle with both title-lookup providers down returned a nil error (F13)")
	}
}

// TestResolveTitleOrdinaryNoMatchStaysNilError is the happy-path guard
// alongside the outage test above: both providers answer fine and simply
// have nothing close enough — that must still read as "nothing found", not
// as an outage.
func TestResolveTitleOrdinaryNoMatchStaysNilError(t *testing.T) {
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Host, "openalex.org"):
			return jsonResponse(r, `{"results":[]}`), nil
		case strings.Contains(r.URL.Host, "crossref.org"):
			return jsonResponse(r, `{"message":{"items":[]}}`), nil
		case strings.Contains(r.URL.Host, "arxiv.org"):
			return response(r, http.StatusOK, "application/atom+xml", "<feed></feed>"), nil
		default:
			t.Fatalf("unexpected host %s", r.URL.Host)
			return nil, nil
		}
	})}
	resolver := &Resolver{Client: client}
	candidates, err := resolver.ResolveTitle(context.Background(), "A Title Nobody Indexed")
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want none", candidates)
	}
	if err != nil {
		t.Fatalf("ResolveTitle with an ordinary no-match returned an error: %v", err)
	}
}
