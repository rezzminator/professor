package harvest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Review-pass regressions for the harvest-perfection wave. Each one was watched
// FAILING against the unfixed branch before its fix landed.

// A contact email must not corrupt the Crossref search query. The bug appended
// withContact's own "?mailto=…" AFTER `&rows=N`, folding the address into the
// rows value so Crossref 400s and search_literature' Crossref widening is dead on every
// host that configures an email — the exact hosts it was added to serve.
func TestReviewFindCrossrefKeepsRowsSeparateFromContact(t *testing.T) {
	var seen string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.URL.String()
		return jsonResponse(r, `{"message":{"items":[]}}`), nil
	})}
	resolver := &Resolver{Client: client, ContactEmail: "ops@example.org"}
	resolver.findCrossref(context.Background(), client, "Attention is all you need", 7)

	parsed, err := url.Parse(seen)
	if err != nil {
		t.Fatalf("findCrossref built an unparseable url %q: %v", seen, err)
	}
	query := parsed.Query()
	if got := query.Get("rows"); got != "7" {
		t.Fatalf("rows=%q (url %q) — the contact email leaked into the rows value", got, seen)
	}
	if got := query.Get("mailto"); got != "ops@example.org" {
		t.Fatalf("mailto=%q (url %q) — the contact email must ride as its own parameter", got, seen)
	}
}

// A pdf link that EXISTS but cannot be rewritten to a fetchable https path is a
// resolution failure. Returning ("", nil) made it indistinguishable from "this
// accession has no pdf-format link at all".
func TestReviewPMCOAPDFURLUnrewritableHrefIsAnErrorNotAbsence(t *testing.T) {
	payload := `<OA><records><record id="PMC7"><link format="pdf" href="gopher://elsewhere.invalid/x.pdf"/></record></records></OA>`
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/xml", payload), nil
	})}
	got, err := PMCOAPDFURL(context.Background(), client, "PMC7")
	if got != "" {
		t.Fatalf("PMCOAPDFURL returned %q for an unfetchable href", got)
	}
	if err == nil {
		t.Fatal("an unfetchable pdf href must surface as an ERROR — ('' , nil) claims no pdf link exists")
	}

	// And the honest absence still reads as absence.
	empty := `<OA><records><record id="PMC7"><link format="tgz" href="ftp://ftp.ncbi.nlm.nih.gov/pub/pmc/a.tar.gz"/></record></records></OA>`
	emptyClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/xml", empty), nil
	})}
	if got, err := PMCOAPDFURL(context.Background(), emptyClient, "PMC7"); got != "" || err != nil {
		t.Fatalf("a record with no pdf link must be a clean absence, got %q err=%v", got, err)
	}
}

// ocrBrokenConverter converts every PDF to empty text and then FAILS to OCR —
// the outage shape, distinct from an OCR pass that genuinely finds no text.
type ocrBrokenConverter struct{}

func (ocrBrokenConverter) Convert(_ context.Context, kind, _ string, _ []byte) (string, error) {
	if kind == "pdf" {
		return "", nil
	}
	return "web body", nil
}

func (ocrBrokenConverter) ConvertOCR(_ context.Context, _, _ string, _ []byte) (string, error) {
	return "", fmt.Errorf("tesseract binary not found")
}

func TestReviewOCRBackendFailureNeverRendersAsEmptyPDF(t *testing.T) {
	pdfTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "application/pdf", "%PDF-1.7 scanned pages"), nil
	})
	missing := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(nil, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		ContactEmail: "test@example.org",
		CacheDir:     t.TempDir(),
		Client:       &http.Client{Transport: pdfTransport},
		Chrome:       &http.Client{Transport: pdfTransport},
		Jina:         &http.Client{Transport: missing},
		OA:           &http.Client{Transport: missing},
		Converter:    ocrBrokenConverter{},
	})
	result := h.Fetch(context.Background(), "https://journals.example.test/scanned.pdf")
	if result.Error == "" {
		t.Fatal("a scan whose OCR backend died must still fail the fetch")
	}
	if strings.Contains(result.Error, "produced nothing") {
		t.Fatalf("a DEAD OCR backend was reported as an OCR pass that found no text: %q", result.Error)
	}
	if !strings.Contains(result.Error, "could not RUN") {
		t.Fatalf("the OCR outage must be named at the visible surface: %q", result.Error)
	}
}

// The exhausted-DOI receipt lists the sources actually queried. Unpaywall is
// gated on an operator email, so a keyless run naming it is a false claim.
func TestReviewExhaustedDOIReceiptNamesOnlyQueriedSources(t *testing.T) {
	missing := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(nil, http.StatusNotFound, "application/json", `{}`), nil
	})
	newHarvester := func(email string) *Harvester {
		return mustNew(t, Options{
			ContactEmail: email,
			CacheDir:     t.TempDir(),
			Client:       &http.Client{Transport: missing},
			Chrome:       &http.Client{Transport: missing},
			Jina:         &http.Client{Transport: missing},
			OA:           &http.Client{Transport: missing},
		})
	}

	keyless := newHarvester("").Fetch(context.Background(), "10.9999/no-copy")
	if keyless.Error == "" {
		t.Fatal("an unresolvable DOI must fail")
	}
	if strings.Contains(keyless.Error, "checked Unpaywall") {
		t.Fatalf("a keyless run claimed it checked Unpaywall, which it SKIPS: %q", keyless.Error)
	}
	if !strings.Contains(keyless.Error, "Unpaywall was SKIPPED") {
		t.Fatalf("the skip must be named, not silently omitted: %q", keyless.Error)
	}

	configured := newHarvester("ops@example.org").Fetch(context.Background(), "10.9999/no-copy")
	if !strings.Contains(configured.Error, "checked Unpaywall") {
		t.Fatalf("a configured run DOES check Unpaywall and must say so: %q", configured.Error)
	}

	// Crossref is queried on every DOI (no key, no email gate), so the receipt
	// must name it in both runs; omitting it under-reports what was checked.
	for name, run := range map[string]Result{"keyless": keyless, "configured": configured} {
		if !strings.Contains(run.Error, "Crossref") {
			t.Fatalf("the %s receipt omits Crossref, which every DOI resolution queries: %q", name, run.Error)
		}
	}
}
