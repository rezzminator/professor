package harvest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestBibliographicLandingCycleStopsAfterOneHop(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	const sourceA = "https://publisher.test/a"
	const sourceB = "https://publisher.test/b"
	var aRequests, bRequests int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case sourceA:
			aRequests++
			return response(
				r,
				http.StatusOK,
				"text/html",
				`<html><a class="document-link" href="/b">Full text</a></html>`,
			), nil
		case sourceB:
			bRequests++
			return response(
				r,
				http.StatusOK,
				"text/html",
				`<html><a class="document-link" href="/a">Full text</a></html>`,
			), nil
		default:
			return response(r, http.StatusNotFound, "text/plain", "missing"), nil
		}
	})
	convert := legacyConverterFunc(func(_ context.Context, kind, _ string, _ []byte) (string, error) {
		if kind != "html" {
			return "", errors.New("unexpected kind")
		}
		return "# Abstract\n# Fingerprint\n# Cite this\n", nil
	})
	direct := &http.Client{Transport: transport}
	chrome := &http.Client{Transport: transport}
	h := mustNew(
		t,
		Options{
			CacheDir:  t.TempDir(),
			Client:    direct,
			Chrome:    chrome,
			Jina:      direct,
			OA:        fixtureTwin(direct),
			Converter: convert,
		},
	)
	got := h.Fetch(context.Background(), sourceA)
	if got.Error == "" {
		t.Fatalf("landing cycle unexpectedly fetched content: %#v", got)
	}
	if aRequests > 2 || bRequests > 2 {
		t.Fatalf("landing cycle was followed repeatedly: sourceA=%d sourceB=%d", aRequests, bRequests)
	}
}

func TestBibliographicLandingFollowsFullTextDocumentLink(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	const landingURL = "https://publisher.test/landing"
	const documentURL = "https://publisher.test/files/fulltext.pdf"
	var documentRequests int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case landingURL:
			return response(
				r,
				http.StatusOK,
				"text/html",
				`<html><body><h1>Title</h1><a class="document-link" href="/files/fulltext.pdf">Full text</a></body></html>`,
			), nil
		case documentURL:
			documentRequests++
			return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nfull text\n%%EOF"), nil
		default:
			return response(r, http.StatusNotFound, "text/plain", "missing"), nil
		}
	})
	convert := legacyConverterFunc(func(_ context.Context, kind, _ string, _ []byte) (string, error) {
		if kind == "html" {
			return "# Title\n# Abstract\n# Fingerprint\n# Cite this\n", nil
		}
		return "A Sequential Analysis\n# Discussion\nfull text", nil
	})
	direct := &http.Client{Transport: transport}
	chrome := &http.Client{Transport: transport}
	h := mustNew(
		t,
		Options{CacheDir: t.TempDir(), Client: direct, Chrome: chrome, Jina: direct, OA: direct, Converter: convert},
	)
	got := h.Fetch(context.Background(), landingURL)
	if got.Error != "" || got.Kind != "pdf" || !strings.Contains(got.Content, "A Sequential Analysis") {
		t.Fatalf("bibliographic landing fetch = %#v; want linked full text", got)
	}
	if documentRequests != 1 {
		t.Fatalf("full text document requests = %d, want one bounded landing follow", documentRequests)
	}
}
