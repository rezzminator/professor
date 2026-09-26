package harvest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// landingArticle is a page long enough for the ladder to store.
var landingArticle = strings.Repeat("The hotel sits on the square, its rooms facing the gardens and the "+
	"quiet street behind them, a short walk from the river.\n\n", 8)

// landingHarvester is a Harvester whose HTTP rungs are served by origin and
// whose reader by reader (nil: any reader call fails the test).
func landingHarvester(t *testing.T, origin, reader roundTripFunc) *Harvester {
	t.Helper()
	if reader == nil {
		reader = func(request *http.Request) (*http.Response, error) {
			t.Errorf("a reader or archive was asked for %s", request.URL)
			return response(request, http.StatusServiceUnavailable, "text/plain", "busy"), nil
		}
	}
	converter := &browserSpyConverter{convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
		if strings.Contains(string(body), "Access Denied") {
			return "Access Denied", nil
		}
		return landingArticle, nil
	}}
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: origin},
		Chrome:      &http.Client{Transport: origin},
		Jina:        &http.Client{Transport: reader},
		OA:          &http.Client{Transport: reader},
		Converter:   converter,
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
}

// redirectingOrigin answers every address in hops with a 302 to its value and
// any other address with the article page.
func redirectingOrigin(hops map[string]string) roundTripFunc {
	return func(request *http.Request) (*http.Response, error) {
		if next, ok := hops[request.URL.String()]; ok {
			answer := response(request, http.StatusFound, "text/html", "")
			answer.Header.Set("Location", next)
			return answer, nil
		}
		return response(request, http.StatusOK, "text/html; charset=utf-8",
			"<html><body><article>"+landingArticle+"</article></body></html>"), nil
	}
}

// TestSamePageRedirectsPassSilently: a redirect to the same page — http to
// https, www, a trailing slash, a locale prefix, tracking parameters, a slug
// after an id — is the page, stored with no note; the rung's final address is
// classified, never assumed.
func TestSamePageRedirectsPassSilently(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ requested, final string }{
		{"http://lobste.rs/s/mroowi", "https://lobste.rs/s/mroowi"},
		{"https://example.com/news/story", "https://www.example.com/news/story/"},
		{"https://shop.example.com/item/42", "https://shop.example.com/en-us/item/42?utm_source=feed"},
		{"https://superuser.com/questions/209437", "https://superuser.com/questions/209437/how-do-i-scroll-in-tmux"},
		{"https://example.com/docs/", "https://example.com/docs/index.html"},
	} {
		if got := classifyLanding(ctx, tc.requested, tc.final); got != landedSame {
			t.Errorf("%s -> %s is classified %d, want the same page", tc.requested, tc.final, got)
		}
	}
	const requested = "https://hotels.example.com/hotel/fr/ritz-paris.html"
	h := landingHarvester(t, redirectingOrigin(map[string]string{
		requested: "https://www.hotels.example.com/hotel/fr/ritz-paris.html/?label=gen173nr",
	}), nil)
	result := h.FetchWithOptions(ctx, requested, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("a same-page redirect was not stored clean: error %q, partial %q", result.Error, result.Partial)
	}
}

// TestRedirectToAnotherPageIsNamed: an address the site redirects to another
// of its pages (a dead hotel slug to the city's search results) is stored
// naming both addresses, never silently under the requested one.
func TestRedirectToAnotherPageIsNamed(t *testing.T) {
	const requested = "https://hotels.example.com/hotel/fr/ritz-paris.html"
	h := landingHarvester(t, redirectingOrigin(map[string]string{
		requested: "https://hotels.example.com/searchresults.html?dest_id=-1456928;dest_type=city",
	}), nil)
	result := h.FetchWithOptions(context.Background(), requested, FetchOptions{Refresh: true})
	want := "the site redirected " + requested +
		" to https://hotels.example.com/searchresults.html — a different page (the requested page may no longer exist)"
	if result.Error != "" || !strings.Contains(result.Partial, want) {
		t.Fatalf("the redirect was not named: error %q, partial %q, want %q", result.Error, result.Partial, want)
	}
}

// TestRedirectToLoginIsANamedFailure: an address redirecting to a login page
// (or the site's home) is no page: the fetch fails naming the redirect, and no
// reader or archive copy stands in for it.
func TestRedirectToLoginIsANamedFailure(t *testing.T) {
	const requested = "https://hotels.example.com/hotel/fr/ritz-paris.html"
	for _, tc := range []struct{ landing, want string }{
		{"https://account.hotels.example.com/auth/oauth2?client_id=placeholder", "— a login page"},
		{"https://hotels.example.com/index.html?label=gen173nr", "— the site's home page"},
	} {
		h := landingHarvester(t, redirectingOrigin(map[string]string{requested: tc.landing}), nil)
		result := h.FetchWithOptions(context.Background(), requested, FetchOptions{Refresh: true})
		if result.Error == "" || result.Path != "" {
			t.Fatalf("a redirect to %s was stored (partial %q)", tc.landing, result.Partial)
		}
		if !strings.Contains(result.Error, "the site redirected "+requested) ||
			!strings.Contains(result.Error, tc.want) {
			t.Fatalf("the failure does not name the redirect to %s: %q", tc.landing, result.Error)
		}
	}
}

// TestReaderReportedRedirectIsNamed: a reader follows the site's redirect on
// its own side and reports only the address it was asked for; the page's
// canonical address in the reader's HTML (og:url, captured from the reader's
// answer for a dead hotel slug) names where it landed. A page naming no
// canonical address leaves the redirect unknown, and that is named.
func TestReaderReportedRedirectIsNamed(t *testing.T) {
	const requested = "https://hotels.example.com/hotel/fr/ritz-paris.html"
	markdown := "Title: Help! Which property is best?\n\nURL Source: " + requested +
		"\n\nMarkdown Content:\n" + landingArticle
	for _, tc := range []struct{ head, want string }{
		{
			`<meta property="og:url" content="https://hotels.example.com/searchresults.html?dest_id=-1456928;dest_type=city">`,
			"the reader was answered for " + requested +
				" from https://hotels.example.com/searchresults.html — a different page",
		},
		{"", "the page names no canonical address: a redirect to another page could not be ruled out"},
	} {
		origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return response(request, http.StatusForbidden, "text/html", "<html><body>Access Denied</body></html>"), nil
		})
		reader := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get(readerHTMLFormat) == "html" {
				return response(request, http.StatusOK, "text/html; charset=utf-8",
					"<html><head>"+tc.head+"</head><body><article>"+landingArticle+"</article></body></html>"), nil
			}
			return response(request, http.StatusOK, "text/plain; charset=utf-8", markdown), nil
		})
		h := landingHarvester(t, origin, reader)
		result := h.FetchWithOptions(context.Background(), requested, FetchOptions{Refresh: true})
		if result.Error != "" || result.Method != "jina" || !strings.Contains(result.Partial, tc.want) {
			t.Fatalf("the reader's landing was not named (method %q, error %q): partial %q, want %q",
				result.Method, result.Error, result.Partial, tc.want)
		}
	}
}

// TestReaderLandingOnTheSamePageIsSilent: a canonical address naming the
// requested page names nothing.
func TestReaderLandingOnTheSamePageIsSilent(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(
		`<html><head><link rel="canonical" href="/2024/Dec/31/llms-in-2024/"></head></html>`))
	if err != nil {
		t.Fatal(err)
	}
	const source = "https://simonwillison.net/2024/Dec/31/llms-in-2024"
	if got := readerLanding(context.Background(), source, doc); got != "" {
		t.Fatalf("a reader page at its canonical address was flagged: %q", got)
	}
}

// canonicalReader is reader with its HTML answers naming the page the request
// asks for as their canonical address, as a reader's HTML of a page does: a
// test about another check than readerLanding serves a page that names itself.
func canonicalReader(reader roundTripFunc) roundTripFunc {
	return func(request *http.Request) (*http.Response, error) {
		answer, err := reader(request)
		if err != nil || request.Header.Get(readerHTMLFormat) != "html" {
			return answer, err
		}
		body, err := io.ReadAll(answer.Body)
		if err != nil {
			return nil, err
		}
		target := request.URL.String()
		if at := strings.Index(target, "/http"); at >= 0 {
			target = target[at+1:]
		}
		answer.Body = io.NopCloser(strings.NewReader(
			`<link rel="canonical" href="` + html.EscapeString(target) + `">` + string(body)))
		return answer, nil
	}
}
