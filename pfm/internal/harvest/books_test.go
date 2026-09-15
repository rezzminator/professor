package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestPreferredTextFormatPrefersHTMLOverPlainAndSortsMIMEKeys(t *testing.T) {
	cases := []struct {
		name     string
		formats  map[string]string
		wantURL  string
		wantKind string
	}{
		{
			name: "html beats plain text when both present",
			formats: map[string]string{
				"text/plain; charset=utf-8": "https://g.example/book.txt",
				"text/html; charset=utf-8":  "https://g.example/book.html",
			},
			wantURL:  "https://g.example/book.html",
			wantKind: "html",
		},
		{
			name:     "plain text used when no html present",
			formats:  map[string]string{"text/plain; charset=utf-8": "https://g.example/book.txt"},
			wantURL:  "https://g.example/book.txt",
			wantKind: "txt",
		},
		{
			name: "lexicographically first matching html MIME wins",
			formats: map[string]string{
				"text/html; charset=us-ascii": "https://g.example/ascii.html",
				"text/html; charset=utf-8":    "https://g.example/utf8.html",
			},
			wantURL:  "https://g.example/ascii.html",
			wantKind: "html",
		},
		{
			name:     "no text formats yields nothing",
			formats:  map[string]string{"application/epub+zip": "https://g.example/book.epub"},
			wantURL:  "",
			wantKind: "",
		},
		{
			name:     "empty-string value for the matching MIME is skipped",
			formats:  map[string]string{"text/html; charset=utf-8": ""},
			wantURL:  "",
			wantKind: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotKind := preferredTextFormat(tc.formats)
			if gotURL != tc.wantURL || gotKind != tc.wantKind {
				t.Fatalf(
					"preferredTextFormat(%#v) = (%q, %q), want (%q, %q)",
					tc.formats,
					gotURL,
					gotKind,
					tc.wantURL,
					tc.wantKind,
				)
			}
		})
	}
}

// TestResolveBookOrdersISBNCandidatesByPriorityAndDedupes drives ResolveBook
// through a fake transport (no network) for an ISBN query and pins the
// candidate shape and priority ordering: two same-priority Internet Archive
// files ahead of a lower-priority HathiTrust volume, sorted by Priority
// ascending, per sortCandidates.
func TestResolveBookOrdersISBNCandidatesByPriorityAndDedupes(t *testing.T) {
	const isbn = "9780306406157"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		switch {
		case strings.Contains(u, "library.oapen.org/rest/search"):
			return jsonResponse(r, `[]`), nil
		case strings.Contains(u, "openlibrary.org/isbn/"):
			return jsonResponse(r, `{"ocaid":"testocaid"}`), nil
		case strings.Contains(u, "archive.org/metadata/testocaid"):
			return jsonResponse(
				r,
				`{"metadata":{"access-restricted-item":"false"},"files":[{"name":"book.pdf"},{"name":"book_djvu.txt"}]}`,
			), nil
		case strings.Contains(u, "directory.doabooks.org"):
			return jsonResponse(r, `[]`), nil
		case strings.Contains(u, "catalog.hathitrust.org/api/volumes/brief/isbn/"):
			return jsonResponse(
				r,
				`{"items":[{"usRightsString":"Full view","itemURL":"https://babel.hathitrust.org/cgi/pt?id=test.vol1"}]}`,
			), nil
		default:
			t.Fatalf("unexpected request to %s", u)
			return nil, nil
		}
	})}
	resolver := &Resolver{Client: client}
	got, err := resolver.ResolveBook(context.Background(), isbn)
	if err != nil {
		t.Fatalf("ResolveBook error: %v", err)
	}
	wantURLs := []string{
		"https://archive.org/download/testocaid/book.pdf",
		"https://archive.org/download/testocaid/book_djvu.txt",
		"https://babel.hathitrust.org/cgi/pt?id=test.vol1",
	}
	if len(got) != len(wantURLs) {
		t.Fatalf("ResolveBook candidates=%#v, want %d entries matching %v", got, len(wantURLs), wantURLs)
	}
	for i, want := range wantURLs {
		if got[i].URL != want {
			t.Fatalf("ResolveBook candidate[%d].URL = %q, want %q (full=%#v)", i, got[i].URL, want, got)
		}
	}
	if got[0].Source != "internetarchive" || got[0].Kind != "pdf" || got[0].Priority != 18 {
		t.Fatalf("ResolveBook pdf candidate=%#v, want internetarchive/pdf priority 18", got[0])
	}
	if got[2].Source != "hathitrust" || got[2].Kind != "html" || got[2].Free != "pd" {
		t.Fatalf("ResolveBook hathitrust candidate=%#v, want hathitrust/html free=pd", got[2])
	}
	if got[0].Priority > got[2].Priority {
		t.Fatalf("ResolveBook candidates not sorted by ascending priority: %#v", got)
	}
}

// TestResolverHathitrustKeepsOnlyFullViewVolumes pins the legality gate:
// HathiTrust items report the same ISBN under mixed rights strings, and only
// "Full view" (public domain) items may become candidates — a lending-only
// "Search only" hit must never leak through as a downloadable copy.
func TestResolverHathitrustKeepsOnlyFullViewVolumes(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.String(), "catalog.hathitrust.org/api/volumes/brief/isbn/9780306406157.json") {
			t.Fatalf("unexpected hathitrust request: %s", r.URL)
		}
		return jsonResponse(r, `{"items":[
			{"usRightsString":"Search only","itemURL":"https://babel.hathitrust.org/cgi/pt?id=locked.vol1"},
			{"usRightsString":"Full view","itemURL":"https://babel.hathitrust.org/cgi/pt?id=open.vol2"}
		]}`), nil
	})}
	resolver := &Resolver{}
	got, err := resolver.hathitrust(context.Background(), client, "978-0-306-40615-7")
	if err != nil {
		t.Fatalf("hathitrust error: %v", err)
	}
	if len(got) != 1 || got[0].URL != "https://babel.hathitrust.org/cgi/pt?id=open.vol2" {
		t.Fatalf("hathitrust candidates=%#v, want exactly the Full view volume", got)
	}
}

// TestResolverHathitrustTreatsFailedLookupAsNoCopyNotAbsence pins the outage
// gate: a non-2xx HathiTrust response returns (nil, nil) — no error bubbled
// to the caller, and no candidate fabricated — matching the log comment
// distinguishing "no full-view volume exists" from "the lookup failed".
func TestResolverHathitrustTreatsFailedLookupAsNoCopyNotAbsence(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Body:       http.NoBody,
			Request:    r,
			Header:     http.Header{},
		}, nil
	})}
	resolver := &Resolver{}
	got, err := resolver.hathitrust(context.Background(), client, "9780306406157")
	if err != nil || got != nil {
		t.Fatalf("hathitrust(failed lookup) = (%#v, %v), want (nil, nil)", got, err)
	}
}

// TestResolverHathitrustRefusesOversizeBodyByName is the representative
// regression for task 2: hathitrust() previously read its response through
// getBody (oversizeTruncate: true), so an over-ceiling JSON response was
// silently truncated and then failed json.Unmarshal — indistinguishable from
// "malformed JSON" when the real story is the byte ceiling. Routed through
// getJSONBody (oversizeTruncate: false), the failure must name the ceiling.
func TestResolverHathitrustRefusesOversizeBodyByName(t *testing.T) {
	oversize := strings.Repeat("a", 10<<20+1<<20)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(
			r,
			`{"items":[{"usRightsString":"Full view","itemURL":"https://babel.hathitrust.org/cgi/pt?id=`+oversize+`"}]}`,
		), nil
	})}
	resolver := &Resolver{}
	got, err := resolver.hathitrust(context.Background(), client, "9780306406157")
	if got != nil {
		t.Fatalf("hathitrust(oversize) candidates = %#v, want nil", got)
	}
	// hathitrust treats every lookup failure (outage AND oversize alike) as
	// "no copy" rather than bubbling an error — TestResolverHathitrustTreats
	// FailedLookupAsNoCopyNotAbsence pins that contract. The byte-ceiling
	// proof therefore lives at the shared helper directly: it fails outright,
	// and its error must name the ceiling rather than describe a decode
	// failure, whichever caller reaches it.
	if err != nil {
		t.Fatalf("hathitrust(oversize) error = %v, want nil (outage semantics)", err)
	}
	var data any
	getErr := getJSONBody(
		context.Background(),
		client,
		"https://catalog.hathitrust.org/api/volumes/brief/isbn/9780306406157.json",
		defaultUA,
		nil,
		10<<20,
		&data,
	)
	if getErr == nil {
		t.Fatal("getJSONBody(oversize) error = nil, want an oversize refusal")
	}
	if strings.Contains(getErr.Error(), "unexpected end of JSON input") ||
		strings.Contains(getErr.Error(), "decode JSON") {
		t.Fatalf(
			"getJSONBody(oversize) error = %q, want it to name the byte ceiling rather than a decode failure",
			getErr,
		)
	}
	if !strings.Contains(getErr.Error(), "exceeds") {
		t.Fatalf("getJSONBody(oversize) error = %q, want it to name the byte ceiling", getErr)
	}
}

func TestResolverHathitrustSkipsNonISBNQueries(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("hathitrust must not make a network call for a non-ISBN query, requested %s", r.URL)
		return nil, nil
	})}
	resolver := &Resolver{}
	got, err := resolver.hathitrust(context.Background(), client, "not an isbn at all")
	if err != nil || got != nil {
		t.Fatalf("hathitrust(non-ISBN) = (%#v, %v), want (nil, nil)", got, err)
	}
}
