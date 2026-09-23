package harvest

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestPageFullTextLinkFollowsOnlyTheSameOriginPDF: the page's own PDF link is
// the first Markdown link on its origin whose path ends in .pdf — a PDF on
// another origin and a same-origin page link are passed over.
func TestPageFullTextLinkFollowsOnlyTheSameOriginPDF(t *testing.T) {
	page := "https://record.test/rec-1"
	for name, tc := range map[string]struct {
		content string
		want    string
	}{
		"relative pdf": {
			"[Another site's PDF](https://elsewhere.test/other.pdf)\n[Record](/rec-1v1)\n" +
				"[Download](/rec-1v1/file/Paper.PDF#page=1)\n[Later](/rec-1v2/file/later.pdf)",
			"https://record.test/rec-1v1/file/Paper.PDF",
		},
		"absolute pdf with title": {
			`[PDF](<https://record.test/rec-1/document.pdf> "full text")`,
			"https://record.test/rec-1/document.pdf",
		},
		"off-origin only": {"[PDF](https://elsewhere.test/rec-1.pdf)", ""},
		"no link":         {"An abstract with no links at all.", ""},
	} {
		if got := pageFullTextLink(tc.content, page); got != tc.want {
			t.Errorf("%s: pageFullTextLink = %q, want %q", name, got, tc.want)
		}
	}
}

// recordPageHTML is an open-access record page: the abstract and the
// record's metadata, and — when pdf is set — a link to the PDF it holds.
func recordPageHTML(pdf string) string {
	link := ""
	if pdf != "" {
		link = `<p><a href="` + pdf + `">Download the full text (PDF)</a></p>`
	}
	return "<html><head><title>Deep learning</title></head><body><article><h1>Deep learning</h1>" +
		"<h2>Abstract</h2><p>" + strings.Repeat("Deep learning allows computational models to learn representations. ", 14) +
		"</p>" + link + "<h2>Keywords</h2><p>deep learning, representation learning</p></article></body></html>"
}

func recordPageHarvester(t *testing.T, pdf string) *Harvester {
	t.Helper()
	web := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "api.openalex.org":
			return jsonResponse(
				r,
				`{"open_access":{"oa_url":"https://record.test/rec-1","oa_status":"green"},`+
					`"best_oa_location":{"is_oa":true,"landing_page_url":"https://record.test/rec-1","pdf_url":null}}`,
			), nil
		case r.URL.Host == "record.test" && r.URL.Path == "/rec-1":
			return response(r, http.StatusOK, "text/html", recordPageHTML(pdf)), nil
		case r.URL.Host == "record.test" && pdf != "" && r.URL.Path == pdf:
			return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nthe work's full text\n%%EOF"), nil
		}
		return response(r, http.StatusNotFound, "application/json", `{}`), nil
	})
	return mustNew(t, Options{
		CacheDir: t.TempDir(), Client: &http.Client{Transport: web}, Chrome: &http.Client{Transport: web},
		Jina: &http.Client{Transport: web}, OA: &http.Client{Transport: web}, Converter: &anchorConverter{},
	})
}

// TestKnownIDThinOpenAccessPageFollowsItsPDFLink: an open-access candidate
// read as HTML that carries no full text does not end the candidate loop —
// the page's own same-origin PDF link is read and is the answer.
func TestKnownIDThinOpenAccessPageFollowsItsPDFLink(t *testing.T) {
	ctx, _ := captureLogs(t)
	h := recordPageHarvester(t, "/rec-1v1/file/paper.pdf")
	got := h.fetchKnownID(ctx, "10.9999/record-test", IdentifierDOI, FetchOptions{})
	if got.Error != "" {
		t.Fatalf("fetchKnownID error = %q, want the record page's PDF", got.Error)
	}
	if got.Kind != kindPDF || !strings.Contains(got.Content, "converted:https://record.test/rec-1v1/file/paper.pdf") {
		t.Fatalf("kind = %q content = %.300q, want the PDF the record page links", got.Kind, got.Content)
	}
	if got.Partial != "" {
		t.Fatalf("partial = %q, want none: the PDF is the full text", got.Partial)
	}
}

// TestKnownIDThinOpenAccessPageWithNoOtherCopyNamesWhy: with no PDF link and
// no other candidate, the thin page is still the answer, and partial names
// that it carries no full text and that nothing else did.
func TestKnownIDThinOpenAccessPageWithNoOtherCopyNamesWhy(t *testing.T) {
	ctx, _ := captureLogs(t)
	h := recordPageHarvester(t, "")
	got := h.fetchKnownID(ctx, "10.9999/record-test", IdentifierDOI, FetchOptions{})
	if got.Error != "" {
		t.Fatalf("fetchKnownID error = %q, want the thin record page", got.Error)
	}
	if !strings.Contains(got.Content, "Deep learning allows computational models") {
		t.Fatalf("content = %.300q, want the record page", got.Content)
	}
	for _, want := range []string{
		"the open-access copy at https://record.test carries no full text (0 body sections",
		"no other candidate carried full text",
	} {
		if !strings.Contains(got.Partial, want) {
			t.Fatalf("partial = %q, want it to contain %q", got.Partial, want)
		}
	}
}

// anchorConverter renders an HTML page's anchors as Markdown links, as the
// sidecar does, and every other kind as fakeConverter does.
type anchorConverter struct{ fakeConverter }

var anchorPattern = regexp.MustCompile(`<a href="([^"]+)">([^<]*)</a>`)

func (c *anchorConverter) Convert(ctx context.Context, kind, source string, body []byte) (string, error) {
	if kind == "html" {
		return anchorPattern.ReplaceAllString(string(body), "[$2]($1)"), nil
	}
	return c.fakeConverter.Convert(ctx, kind, source, body)
}
