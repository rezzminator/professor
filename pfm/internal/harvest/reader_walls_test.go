package harvest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readerWallFixture reads a captured reader response from testdata/walls.
func readerWallFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "walls", name))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return string(body)
}

// TestReaderPagesPassTheWallCheck: a page only a reader rung served passes
// the same wall check as an HTTP rung's page. A paywalled article Jina served
// as Markdown is stored partial naming the paywall its HTML flags; an article
// whose prose mentions a subscription, its HTML flagging nothing, is stored
// whole; a logged-out app page names its login wall; a check whose HTML the
// reader could not serve is named, never stored as no wall; and a paywall an earlier
// rung's HTML showed is named on the reader's page when the reader's HTML
// cannot be read.
func TestReaderPagesPassTheWallCheck(t *testing.T) {
	t.Parallel()
	const paywallURL = "https://www.bloomberg.com/news/articles/2025-10-22/reddit-sues-perplexity-others-over-alleged-data-scraping"
	walledOrigin := `<html><head><script type="application/ld+json">{"@type":"Article","isAccessibleForFree":false}</script>` +
		`</head><body><p>Access to this page has been denied.</p></body></html>`
	for _, tc := range []struct {
		name, source, origin, markdown, readerHTML, partial string
		emptyHTML                                           bool
	}{
		{
			name:       "a paywalled article the reader served",
			source:     paywallURL,
			origin:     "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:   readerWallFixture(t, "reader-paywalled.md"),
			readerHTML: readerWallFixture(t, "reader-paywalled.html"),
			partial:    paywallReason,
		},
		{
			name:       "a clean article that mentions a subscription",
			source:     "https://simonwillison.net/2024/Dec/31/llms-in-2024/",
			origin:     "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:   readerWallFixture(t, "reader-clean.md"),
			readerHTML: readerWallFixture(t, "reader-clean.html"),
		},
		{
			name:       "a logged-out app page the reader served",
			source:     "https://www.linkedin.com/company/microsoft",
			origin:     "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:   readerWallFixture(t, "reader-loginwall.md"),
			readerHTML: readerWallFixture(t, "reader-loginwall.html"),
			partial:    loginWallReason,
		},
		{
			name:     "a wall check the reader's HTML could not serve",
			source:   paywallURL,
			origin:   "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown: readerWallFixture(t, "reader-paywalled.md"),
			partial:  "its wall check could not run (the page's HTML from the jina reader: it answered HTTP 503)",
		},
		{
			name:       "a reader answering the HTML ask with its Markdown",
			source:     paywallURL,
			origin:     "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:   readerWallFixture(t, "reader-paywalled.md"),
			readerHTML: readerWallFixture(t, "reader-paywalled.md"),
			partial: "its wall check could not run (the page's HTML from the jina reader: it answered with Markdown in place of " +
				"the page's HTML)",
		},
		{
			name:      "a reader answering the HTML ask with an empty body",
			source:    paywallURL,
			origin:    "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:  readerWallFixture(t, "reader-paywalled.md"),
			emptyHTML: true,
			partial:   "its wall check could not run (the page's HTML from the jina reader: it answered with an empty body)",
		},
		{
			name:       "a reader's HTML with whitespace before its byte-order mark",
			source:     paywallURL,
			origin:     "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:   readerWallFixture(t, "reader-paywalled.md"),
			readerHTML: " \n\xef\xbb\xbf \n" + readerWallFixture(t, "reader-paywalled.html"),
			partial:    paywallReason,
		},
		{
			name:     "a paywall only the origin's refused HTML showed",
			source:   paywallURL,
			origin:   walledOrigin,
			markdown: readerWallFixture(t, "reader-paywalled.md"),
			partial:  paywallReason,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusForbidden, "text/html; charset=UTF-8", tc.origin), nil
			})
			htmlAsked := false
			reader := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get(readerHTMLFormat) == "html" {
					htmlAsked = true
					if tc.emptyHTML {
						return response(request, http.StatusOK, "text/html; charset=utf-8", ""), nil
					}
					if tc.readerHTML == "" {
						return response(request, http.StatusServiceUnavailable, "text/plain", "busy"), nil
					}
					return response(request, http.StatusOK, "text/html; charset=utf-8", tc.readerHTML), nil
				}
				return response(request, http.StatusOK, "text/plain; charset=utf-8", tc.markdown), nil
			})
			converter := &browserSpyConverter{convertFn: func(context.Context, string, string, []byte) (string, error) {
				return "Access to this page has been denied.", nil
			}}
			h := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: origin},
				Chrome:      &http.Client{Transport: origin},
				Jina:        &http.Client{Transport: reader},
				OA:          &http.Client{Transport: origin},
				Converter:   converter,
				BrowserRung: browserOff(),
				Clock:       newPacingClock(),
			})
			result := h.FetchWithOptions(context.Background(), tc.source, FetchOptions{Refresh: true})
			if result.Error != "" || result.Method != "jina" {
				t.Fatalf("the reader's page was not stored (method %q): %s", result.Method, result.Error)
			}
			switch {
			case tc.partial == "" && result.Partial != "":
				t.Fatalf("a clean reader article was flagged partial: %q", result.Partial)
			case tc.partial != "" && !strings.Contains(result.Partial, tc.partial):
				t.Fatalf("the reader's page is partial %q, want it to name %q", result.Partial, tc.partial)
			}
			if !htmlAsked {
				t.Fatal("the reader's HTML was never asked for; the wall check did not run on the reader's page")
			}
		})
	}
}

// TestReaderServedSignUpWallIsALoginWall: LinkedIn bounces a signed-out
// reader it will not show a page to onto its sign-up wall, and a reader rung
// serves that wall as the page's Markdown (its HTML ask answered with the
// same Markdown). The wall is never stored as the page: the fetch fails as a
// login wall, while a real LinkedIn page a reader served is still stored.
func TestReaderServedSignUpWallIsALoginWall(t *testing.T) {
	t.Parallel()
	const source = "https://www.linkedin.com/directory/companies"
	origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, 999, "text/html; charset=UTF-8", "<html><body></body></html>"), nil
	})
	wall := readerWallFixture(t, "reader-authwall.md")
	reader := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/plain; charset=utf-8", wall), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: origin},
		Chrome:   &http.Client{Transport: origin},
		Jina:     &http.Client{Transport: reader},
		OA:       &http.Client{Transport: origin},
		Converter: &browserSpyConverter{convertFn: func(context.Context, string, string, []byte) (string, error) {
			return "", nil
		}},
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
	result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
	if result.Error == "" || result.Path != "" {
		t.Fatalf("the sign-up wall was stored as the page (method %q, partial %q)", result.Method, result.Partial)
	}
	if result.ErrorKind != errorKindLogin {
		t.Fatalf("the sign-up wall's kind is %q, want %q: %q", result.ErrorKind, errorKindLogin, result.Error)
	}
	if public := PublicFailureMessage(result); !strings.Contains(public, "sign-in") {
		t.Fatalf("the public message does not name the sign-in wall: %q", public)
	}
	if !linkedInSignUpWall(source, wall) || linkedInSignUpWall(source, readerWallFixture(t, "reader-loginwall.md")) ||
		linkedInSignUpWall("https://example.com/directory/companies", wall) {
		t.Fatal("the sign-up wall is not told apart from a LinkedIn page and from another site's page")
	}
}

// TestReaderPagesPassTheStatedCountAndRecallChecks: a page only a reader rung
// served passes the same stated-count check and recall gate as an HTTP rung's
// page, on the reader's own HTML of it: a thread whose structured data states
// more comments than it carries is stored partial naming both counts, and
// markdown keeping a fraction of the page's visible text names what it kept.
func TestReaderPagesPassTheStatedCountAndRecallChecks(t *testing.T) {
	t.Parallel()
	const source = "https://forum.example.com/t/how-teams-use-ai-4g9c"
	article := strings.Repeat("The newspaper sued two technology companies for copyright infringement on Wednesday, "+
		"opening a new front.\n\n", 8)
	for _, tc := range []struct {
		name, markdown, readerHTML, partial string
	}{
		{
			name:     "a thread stating 50 comments and carrying 2",
			markdown: "# How teams use AI\n\n" + article,
			readerHTML: `<html><head><script type="application/ld+json">{"@type":"DiscussionForumPosting",` +
				`"commentCount":50,"comment":[` + commentItems(2) + `]}</script></head><body><article>` +
				articlePreview + `</article></body></html>`,
			partial: "50 comments stated · 2 loaded",
		},
		{
			name:     "markdown keeping one paragraph of a long page",
			markdown: "# How teams use AI\n\n" + article,
			readerHTML: `<html><body><article>` + articlePreview + strings.Repeat(
				`<p>Readers across the region described how their teams adopted new tools during the long winter.</p>`,
				40) + `</article></body></html>`,
			partial: "the reader's markdown kept",
		},
		{
			name:       "markdown keeping the whole page",
			markdown:   "# How teams use AI\n\n" + article,
			readerHTML: `<html><body><article>` + articlePreview + `</article></body></html>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusForbidden, "text/html; charset=UTF-8",
					"<html><body><p>Access to this page has been denied.</p></body></html>"), nil
			})
			reader := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get(readerHTMLFormat) == "html" {
					return response(request, http.StatusOK, "text/html; charset=utf-8", tc.readerHTML), nil
				}
				return response(request, http.StatusOK, "text/plain; charset=utf-8", tc.markdown), nil
			})
			h := mustNew(t, Options{
				CacheDir: t.TempDir(),
				Client:   &http.Client{Transport: origin},
				Chrome:   &http.Client{Transport: origin},
				Jina:     &http.Client{Transport: canonicalReader(reader)},
				OA:       &http.Client{Transport: origin},
				Converter: &browserSpyConverter{
					convertFn: func(context.Context, string, string, []byte) (string, error) {
						return "Access to this page has been denied.", nil
					},
				},
				BrowserRung: browserOff(),
				Clock:       newPacingClock(),
			})
			result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
			if result.Error != "" || result.Method != "jina" {
				t.Fatalf("the reader's page was not stored (method %q): %s", result.Method, result.Error)
			}
			switch {
			case tc.partial == "" && result.Partial != "":
				t.Fatalf("a whole reader page was flagged partial: %q", result.Partial)
			case tc.partial != "" && !strings.Contains(result.Partial, tc.partial):
				t.Fatalf("the reader's page is partial %q, want it to name %q", result.Partial, tc.partial)
			}
		})
	}
}

// signUpWallHarvester serves source's LinkedIn sign-up wall from the rungs
// serve names and answers every other request by answer (nil: HTTP 503).
func signUpWallHarvester(t *testing.T, serve func(*http.Request) bool,
	answer func(*http.Request) (*http.Response, error),
) *Harvester {
	t.Helper()
	wall := readerWallFixture(t, "reader-authwall.md")
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case serve(request):
			return response(request, http.StatusOK, "text/plain; charset=utf-8", wall), nil
		case answer != nil:
			return answer(request)
		}
		return response(request, http.StatusServiceUnavailable, "text/plain", "busy"), nil
	})
	return mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: transport},
		Chrome:   &http.Client{Transport: transport},
		Jina:     &http.Client{Transport: transport},
		OA:       &http.Client{Transport: transport},
		Converter: &browserSpyConverter{convertFn: func(context.Context, string, string, []byte) (string, error) {
			return strings.Repeat(
				"A stilling well keeps the river gauge's reading steady against the current. ",
				40,
			), nil
		}},
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
}

// TestSignUpWallFailuresNameTheirDecisiveKind: a sign-up wall one rung saw
// never erases a later rung's distinct failure — a timeout stays a timeout —
// and a wall a later reader copy or an archived snapshot served is recorded
// however it arrived: a defuddle copy shorter than the origin's page, or a
// Wayback snapshot judged by the original LinkedIn address.
func TestSignUpWallFailuresNameTheirDecisiveKind(t *testing.T) {
	t.Parallel()
	const source = "https://www.linkedin.com/directory/companies"
	isJina := func(request *http.Request) bool { return strings.Contains(request.URL.Host, "jina") }
	isDefuddle := func(request *http.Request) bool { return request.URL.Host == "defuddle.md" }
	origin := func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "www.linkedin.com" || request.URL.Host == "web.archive.org" {
			return response(request, http.StatusForbidden, "text/html; charset=UTF-8",
				"<html><body><p>Members only.</p></body></html>"), nil
		}
		return response(request, http.StatusNotFound, "text/plain", "not found"), nil
	}
	for _, tc := range []struct {
		name, kind string
		serve      func(*http.Request) bool
		answer     func(*http.Request) (*http.Response, error)
	}{
		{
			name:  "a reader's wall, then a defuddle rung that timed out",
			kind:  errorKindTimeout,
			serve: isJina,
			answer: func(request *http.Request) (*http.Response, error) {
				if isDefuddle(request) {
					return nil, fmt.Errorf("defuddle.md: %w", context.DeadlineExceeded)
				}
				return origin(request)
			},
		},
		{
			name:   "a defuddle copy of the wall shorter than the origin's page",
			kind:   errorKindLogin,
			serve:  isDefuddle,
			answer: origin,
		},
		{
			name: "an archived snapshot of the wall",
			kind: errorKindLogin,
			serve: func(request *http.Request) bool {
				return isJina(request) && strings.Contains(request.URL.Path, "web.archive.org")
			},
			answer: func(request *http.Request) (*http.Response, error) {
				if request.URL.Host == "archive.org" {
					return response(request, http.StatusOK, "application/json",
						`{"archived_snapshots":{"closest":{"available":true,"timestamp":"20250101000000"}}}`), nil
				}
				return origin(request)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := signUpWallHarvester(t, tc.serve, tc.answer)
			result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
			if result.Error == "" || result.Path != "" {
				t.Fatalf(
					"the sign-up wall was stored as the page (method %q, partial %q)",
					result.Method,
					result.Partial,
				)
			}
			if result.ErrorKind != tc.kind {
				t.Fatalf("the failure's kind is %q, want %q: %q", result.ErrorKind, tc.kind, result.Error)
			}
			if !strings.Contains(result.Error, "login wall") {
				t.Fatalf("the failure does not name the login wall: %q", result.Error)
			}
		})
	}
}

// TestReaderCheckFailureNamesTheReaderAsked: the checks always ask the jina
// reader for the page's HTML, whichever rung stored the page. When defuddle
// stored it (HTTP 200) and jina refused both asks (HTTP 403), the page's
// http_status stays the stored page's 200, and its gaps name the jina reader as
// the one that answered 403, never "the reader", which would read as the
// defuddle reader that delivered the page.
func TestReaderCheckFailureNamesTheReaderAsked(t *testing.T) {
	t.Parallel()
	const source = "https://www.linkedin.com/company/microsoft"
	markdown := readerWallFixture(t, "reader-loginwall.md")
	origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "defuddle.md" {
			return response(request, http.StatusOK, "text/markdown; charset=utf-8", markdown), nil
		}
		return response(request, http.StatusForbidden, "text/html; charset=UTF-8",
			"<html><body><p>Access to this page has been denied.</p></body></html>"), nil
	})
	jina := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/plain", "forbidden"), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: origin},
		Chrome:   &http.Client{Transport: origin},
		Jina:     &http.Client{Transport: jina},
		OA:       &http.Client{Transport: origin},
		Converter: &browserSpyConverter{convertFn: func(context.Context, string, string, []byte) (string, error) {
			return "Access to this page has been denied.", nil
		}},
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
	result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
	if result.Error != "" || result.Method != "defuddle-reader" {
		t.Fatalf("the defuddle reader's page was not stored (method %q): %s", result.Method, result.Error)
	}
	if result.HTTPStatus != http.StatusOK {
		t.Fatalf("http_status is %d, want the stored page's %d", result.HTTPStatus, http.StatusOK)
	}
	for _, want := range []string{
		"its wall check could not run (the page's HTML from the jina reader: it answered HTTP 403)",
		"the page's HTML from the jina reader could not be read (it answered HTTP 403)",
	} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the gaps do not name the jina reader as the 403's source; want %q in %q", want, result.Partial)
		}
	}
	if strings.Contains(result.Partial, "the reader answered") {
		t.Fatalf("the gaps blame an unnamed reader, read as the defuddle reader that answered 200: %q", result.Partial)
	}
}
