package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestWavePlosNberOfflineDeterministic(t *testing.T) {
	plos := plosCandidates("10.1371/journal.pcbi.1003285")
	if len(plos) != 1 ||
		plos[0].URL != "https://journals.plos.org/pcbi/article/file?id=10.1371%2Fjournal.pcbi.1003285&type=printable" &&
			plos[0].URL != "https://journals.plos.org/pcbi/article/file?id=10.1371/journal.pcbi.1003285&type=printable" {
		t.Fatalf("plos candidates=%#v", plos)
	}
	if plosCandidates("10.1038/s41586-020-2649-2") != nil {
		t.Fatalf("non-PLOS DOI must yield nothing")
	}
	nber := nberCandidates("10.3386/W33186")
	if len(nber) != 1 || nber[0].URL != "https://www.nber.org/system/files/working_papers/w33186/w33186.pdf" {
		t.Fatalf("nber candidates=%#v", nber)
	}
	if nberCandidates("10.3386/not-a-wp-id") != nil {
		t.Fatalf("malformed NBER id must yield nothing")
	}
}

func TestWaveUnpaywallSkipsKeylessRuns(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(r, `{}`), nil
	})}
	resolver := &Resolver{Client: client} // no ContactEmail — keyless run
	cands, err := resolver.unpaywall(context.Background(), client, "10.1234/example")
	if err != nil || cands != nil || calls != 0 {
		t.Fatalf("keyless unpaywall must SKIP cleanly: cands=%#v calls=%d err=%v", cands, calls, err)
	}
}

func TestWaveZenodoOnlyMatchingRecordDocumentFiles(t *testing.T) {
	payload := `{"hits":{"hits":[
		{"doi":"10.5281/zenodo.13235113","files":[
			{"key":"article.pdf","links":{"self":"https://zenodo.org/api/records/13235113/files/article.pdf/content"}},
			{"key":"dataset.zip","links":{"self":"https://zenodo.org/api/records/13235113/files/dataset.zip/content"}}]},
		{"doi":"10.5281/zenodo.99999999","files":[
			{"key":"other.epub","links":{"self":"https://zenodo.org/api/records/9/files/other.epub/content"}}]}]}}`
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(r, payload), nil
	})}
	resolver := &Resolver{Client: client}
	got, err := resolver.zenodo(context.Background(), client, "10.5281/zenodo.13235113")
	if err != nil || len(got) != 1 || !strings.HasSuffix(got[0].URL, "files/article.pdf/content") {
		t.Fatalf("zenodo candidates=%#v err=%v", got, err)
	}
}

func TestLegacyOAProviderEdgeShapes(t *testing.T) {
	t.Run("Unpaywall keeps alternate OA locations", func(t *testing.T) {
		client := legacyOAClient(t, func(*http.Request) string {
			return `{"is_oa":true,"oa_status":"gold","best_oa_location":{"url_for_pdf":"https://public.example.test/best.pdf","version":"publishedVersion"},"oa_locations":[{"url_for_pdf":"https://public.example.test/alternate.pdf","version":"acceptedVersion"}]}`
		})
		got, err := (&Resolver{ContactEmail: "test@example.org"}).unpaywall(
			context.Background(),
			client,
			"10.1234/example",
		)
		if err != nil || len(got) != 2 || got[1].URL != "https://public.example.test/alternate.pdf" ||
			got[1].Priority != 17 {
			t.Fatalf("Unpaywall candidates=%#v err=%v", got, err)
		}
	})

	t.Run("OpenAlex only keeps OA PDF locations", func(t *testing.T) {
		client := legacyOAClient(t, func(*http.Request) string {
			return `{"open_access":{"is_oa":true,"oa_status":"green","oa_url":"https://public.example.test/top.pdf"},"locations":[{"is_oa":false,"pdf_url":"https://closed.example.test/not-oa.pdf"},{"is_oa":true,"pdf_url":"https://public.example.test/location.pdf","version":"acceptedVersion"}]}`
		})
		got, err := (&Resolver{}).openAlexDOI(context.Background(), client, "10.1234/example")
		if err != nil || len(got) != 2 || got[1].URL != "https://public.example.test/location.pdf" ||
			got[1].Priority != 18 {
			t.Fatalf("OpenAlex candidates=%#v err=%v", got, err)
		}
	})

	t.Run("Semantic Scholar normalizes PMC identifiers", func(t *testing.T) {
		client := legacyOAClient(t, func(*http.Request) string {
			return `{"openAccessPdf":null,"externalIds":{"PubMedCentral":"10450651"}}`
		})
		got, err := (&Resolver{}).semanticScholar(context.Background(), client, "10.1234/example")
		if err != nil || len(got) != 1 || got[0].URL != "https://europepmc.org/articles/PMC10450651?pdf=render" {
			t.Fatalf("Semantic Scholar candidates=%#v err=%v", got, err)
		}
	})

	t.Run("DOAJ only consumes the first result", func(t *testing.T) {
		client := legacyOAClient(t, func(*http.Request) string {
			return `{"results":[{"bibjson":{"link":[{"type":"fulltext","url":"https://public.example.test/first"}]}},{"bibjson":{"link":[{"type":"fulltext","url":"https://public.example.test/second"}]}}]}`
		})
		got, err := (&Resolver{}).doaj(context.Background(), client, "10.1234/example")
		if err != nil || len(got) != 1 || got[0].URL != "https://public.example.test/first" {
			t.Fatalf("DOAJ candidates=%#v err=%v", got, err)
		}
	})
}
