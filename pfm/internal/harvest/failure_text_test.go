package harvest

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// staleToolNames are the retired tool names a failure message must never send
// a caller to.
var staleToolNames = []string{"`fetch`", "`search`", "searchCache", "fetchImage", "`archive`"}

// TestPublicFailureMessageNamesCauseAndNextStep is the failure-class table: each
// class a target can end in renders its named cause and what the caller can do,
// never the generic "Retrieval failed. Retry or choose another work.".
func TestPublicFailureMessageNamesCauseAndNextStep(t *testing.T) {
	ladder := []string{"direct", "chrome-impersonation", "browser"}
	cases := []struct {
		name   string
		result Result
		want   []string
		refuse []string
	}{
		{
			"timeout",
			Result{ErrorKind: errorKindTimeout, Rungs: ladder[:2]},
			[]string{"did not answer in time", "Rungs tried: direct, chrome-impersonation", "Retry later"},
			nil,
		},
		{
			"dns",
			Result{ErrorKind: errorKindDNS, HTTPStatus: http.StatusBadGateway},
			[]string{"does not resolve", "Check the URL"},
			nil,
		},
		{
			"tls",
			Result{Error: "tls: failed to verify certificate: x509: certificate signed by unknown authority"},
			[]string{"TLS", "another copy"},
			nil,
		},
		{
			"connect",
			Result{ErrorKind: errorKindConnect},
			[]string{"connection", "Retry later"},
			nil,
		},
		{
			"challenge",
			Result{
				ErrorKind: errorKindChallenge,
				Challenge: true,
				Error:     "blocked by a Cloudflare challenge",
				Rungs:     ladder,
			},
			[]string{
				"Cloudflare",
				"never solves",
				"Rungs tried: direct, chrome-impersonation, browser",
				"another copy",
			},
			nil,
		},
		{
			"login",
			Result{Error: "x shows nothing but a login wall signed-out: no content is visible without an account."},
			[]string{"sign-in", "never signs in"},
			nil,
		},
		{
			"paywall",
			Result{Error: "the article is behind a paywall"},
			[]string{"paywall", "never signs in", "findWorks"},
			nil,
		},
		{
			"ratelimit",
			Result{HTTPStatus: http.StatusTooManyRequests},
			[]string{"rate-limit", "HTTP 429", "Retry later"},
			nil,
		},
		{
			"forbidden",
			Result{HTTPStatus: http.StatusForbidden, Rungs: ladder},
			[]string{"HTTP 403", "never signs in", "another copy"},
			nil,
		},
		{
			"not found",
			Result{ErrorKind: "http", HTTPStatus: http.StatusNotFound, Rungs: ladder},
			[]string{"HTTP 404", "cannot tell", "Rungs tried: direct, chrome-impersonation, browser", "Check the URL"},
			[]string{"wall"},
		},
		{
			"gone",
			Result{HTTPStatus: http.StatusGone},
			[]string{"HTTP 410", "removed", "another copy"},
			nil,
		},
		{
			"server error",
			Result{HTTPStatus: http.StatusServiceUnavailable},
			[]string{"server error", "HTTP 503", "Retry later"},
			nil,
		},
		{
			"too large",
			Result{ErrorKind: errorKindTooLarge},
			[]string{"larger than", "download"},
			nil,
		},
		{
			"unsupported",
			Result{ErrorKind: "unsupported", Kind: "odt"},
			[]string{"odt", "does not read"},
			nil,
		},
		{
			"empty",
			Result{Error: "conversion produced no usable content"},
			[]string{"no readable content", "another copy"},
			nil,
		},
		{
			"converter error",
			Result{ErrorKind: errorKindConversion},
			[]string{"could not be converted", "download"},
			nil,
		},
		{
			"app shell",
			Result{ErrorKind: "app_shell"},
			[]string{"JavaScript app shell", "another copy"},
			nil,
		},
		{
			"no open copy",
			Result{
				Error: "Found DOI 10.1234/x, but no free, legal full text exists in the configured open-access sources. The paper is likely paywalled",
				Rungs: []string{"oa:unpaywall", "oa:core"},
			},
			[]string{"No open copy", "never signs in", "findWorks", "oa-mirror(2 sources)"},
			nil,
		},
		{
			"disabled",
			Result{ErrorKind: errorKindDisabled},
			[]string{"disabled on this harvester", "findWorks"},
			nil,
		},
		{
			"local missing",
			Result{Source: "/data/notes/report.odt", ErrorKind: errorKindMissing},
			[]string{"local file", "parseLocalDocuments"},
			[]string{"/data/notes"},
		},
		{
			"unknown",
			Result{Error: "strange failure at https://secret.example/x in /srv/private/file.bin"},
			[]string{"could not classify", "strange failure"},
			[]string{"secret.example", "/srv/private"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PublicFailureMessage(tc.result)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("message lacks %q:\n%s", want, got)
				}
			}
			for _, refuse := range append(append([]string{"Retry or choose another work"}, tc.refuse...), staleToolNames...) {
				if strings.Contains(got, refuse) {
					t.Errorf("message carries %q:\n%s", refuse, got)
				}
			}
		})
	}
}

// TestLocalUnsupportedFormatsAreNamedFailures: an .odt, a .webarchive and a
// .zip each end in a failure naming the format — never the generic string and
// never a binary body stored as text.
func TestLocalUnsupportedFormatsAreNamedFailures(t *testing.T) {
	root := t.TempDir()
	zipped := func(name, member, body string) []byte {
		var buf bytes.Buffer
		writer := zip.NewWriter(&buf)
		entry, err := writer.Create(member)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("zip %s: %v", name, err)
		}
		return buf.Bytes()
	}
	files := map[string][]byte{
		"sample.odt":        zipped("odt", "mimetype", "application/vnd.oasis.opendocument.text"),
		"sample.zip":        zipped("zip", "a.txt", "hello"),
		"sample.webarchive": append([]byte("bplist00\xd1\x01\x02_\x10\x0fWebMainResource"), make([]byte, 40)...),
	}
	harvester := mustNew(t, Options{CacheDir: t.TempDir(), LocalRoots: []string{root}})
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		result := harvester.FetchPublic(context.Background(), path, FetchOptions{Refresh: true})
		format := strings.TrimPrefix(filepath.Ext(name), ".")
		if result.Error == "" || result.Content != "" {
			t.Errorf("%s: want a named failure, got success %#v", name, result)
			continue
		}
		for _, want := range []string{format, "does not read", "exists at its path"} {
			if !strings.Contains(result.Error, want) {
				t.Errorf("%s: failure lacks %q: %s", name, want, result.Error)
			}
		}
		if result.ErrorKind != "unsupported" {
			t.Errorf("%s: error_kind = %q, want %q", name, result.ErrorKind, "unsupported")
		}
		if strings.Contains(result.Error, root) {
			t.Errorf("%s: public failure exposes the local path: %s", name, result.Error)
		}
	}
}

// TestLadderBrowserNotesNeverContradictTheStatus: the ladder's browser
// sentence calls a failure a wall only when a challenge was seen, and a host
// that does not resolve is never an SSRF refusal.
func TestLadderBrowserNotesNeverContradictTheStatus(t *testing.T) {
	if note := browserRanNote(
		false,
		http.StatusNotFound,
	); strings.Contains(note, "wall") ||
		!strings.Contains(note, "cannot tell") {
		t.Errorf("404 browser note = %q; want no wall, the 404 ambiguity named", note)
	}
	if note := browserRanNote(true, http.StatusForbidden); !strings.Contains(note, "wall") {
		t.Errorf("challenge browser note = %q; want the wall named", note)
	}
	if note := browserRefusedNote(
		errorKindDNS,
	); strings.Contains(note, "SSRF") ||
		!strings.Contains(note, "does not resolve") {
		t.Errorf("DNS browser note = %q; want the unresolved host, not an SSRF refusal", note)
	}
}

// TestFindWorksRanksTheOriginalAboveAReRegistration mirrors the live answer
// for "Attention Is All You Need": OpenAlex returns only a 2025 re-registration
// (open, carrying the original's citations) and arXiv returns the 2017
// original. The original ranks first.
func TestFindWorksRanksTheOriginalAboveAReRegistration(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.openalex.org":
			return response(r, http.StatusOK, "application/json", `{"results":[
{"doi":"https://doi.org/10.65215/2q58a426","display_name":"Attention Is All You Need","open_access":{"oa_status":"gold","oa_url":"https://preprints.example/download/10/108"},"publication_year":2025,"cited_by_count":7660}]}`), nil
		case "export.arxiv.org":
			return response(r, http.StatusOK, "application/atom+xml", `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>http://arxiv.org/abs/1706.03762v7</id><published>2017-06-12T17:57:34Z</published><title>Attention Is All You Need</title></entry></feed>`), nil
		}
		return response(r, http.StatusOK, "application/json", `{}`), nil
	})}
	candidates, err := (&Resolver{Client: client}).FindWorks(context.Background(), "Attention Is All You Need", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) < 2 {
		t.Fatalf("want both records, got %#v", candidates)
	}
	if first := candidates[0]; !strings.Contains(first.URL, "1706.03762") || first.Year != 2017 {
		t.Fatalf("first candidate = %s (%d), want the 2017 arXiv original: %#v", first.URL, first.Year, candidates)
	}
}
