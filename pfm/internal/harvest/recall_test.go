package harvest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// catalogPage is a non-forum page whose content is a grid of custom-element
// cards — the shape a main-content extractor classifies as boilerplate.
func catalogPage() string {
	var b strings.Builder
	b.WriteString(`<html><head><title>Field guide</title></head><body><nav><a href="/">Home</a></nav><main>`)
	b.WriteString(`<h1>Field guide to coastal birds</h1><p>Every species recorded on the estuary this season.</p>`)
	for index := 0; index < 40; index++ {
		fmt.Fprintf(&b, `<species-card data-id="%d"><h3>Species %d</h3><p>Nests in the dunes, feeds on the mudflats `+
			`at low tide and winters along the southern shore.</p></species-card>`, index, index)
	}
	b.WriteString(`</main><footer>Estuary trust</footer></body></html>`)
	return b.String()
}

// leadOnlyConverter keeps the heading and lede — what a main-content
// extractor returns for a card grid it reads as boilerplate.
func leadOnlyConverter() Converter {
	return legacyConverterFunc(func(_ context.Context, kind, _ string, raw []byte) (string, error) {
		if kind != kindHTML {
			return string(raw), nil
		}
		return "# Field guide to coastal birds\n\nEvery species recorded on the estuary this season. " +
			strings.Repeat("Recorded on the estuary. ", 30), nil
	})
}

// fullDOMSpy is a converter whose main-content path keeps the lead only and
// whose full-DOM path is scripted.
type fullDOMSpy struct {
	Converter
	full  func(body []byte) (string, error)
	calls int
}

func (spy *fullDOMSpy) ConvertFullDOM(_ context.Context, _ string, body []byte) (string, error) {
	spy.calls++
	return spy.full(body)
}

func TestRecallGateFallsBackToTheFullDOM(t *testing.T) {
	spy := &fullDOMSpy{Converter: leadOnlyConverter(), full: func(body []byte) (string, error) {
		return tagStripConverter().Convert(context.Background(), kindHTML, "", body)
	}}
	h := pageHarvester(t, catalogPage(), spy, browserOff())
	result := h.Fetch(context.Background(), "https://guide.example.test/birds")
	if result.Error != "" || spy.calls != 1 {
		t.Fatalf("low-recall extraction did not fall back to the full DOM: calls=%d error=%q", spy.calls, result.Error)
	}
	if !strings.Contains(result.Content, "Species 39") || result.Partial != "" {
		t.Fatalf("full-DOM conversion not used as the complete artifact: partial=%q content=%.300q",
			result.Partial, result.Content)
	}
}

func TestRecallGateFlagsATruncatedPageWhereTheReaderSeesIt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		converter Converter
		reason    string
	}{
		{"no full-DOM converter", leadOnlyConverter(), "no full-DOM converter is wired"},
		{"full-DOM conversion fails", &fullDOMSpy{
			Converter: leadOnlyConverter(),
			full:      func([]byte) (string, error) { return "", errors.New("markitdown exploded") },
		}, "the full-DOM conversion failed: conversion error"},
		{"full-DOM conversion no better", &fullDOMSpy{
			Converter: leadOnlyConverter(),
			full:      func([]byte) (string, error) { return "Field guide", nil },
		}, "the full-DOM conversion kept only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := pageHarvester(t, catalogPage(), tc.converter, browserOff())
			result := h.Fetch(context.Background(), "https://guide.example.test/birds")
			if result.Error != "" {
				t.Fatalf("truncated page failed instead of being flagged: %q", result.Error)
			}
			if !strings.Contains(result.Partial, "main-content extraction kept") ||
				!strings.Contains(result.Partial, tc.reason) {
				t.Fatalf("a truncated page was reported as a clean success: partial=%q", result.Partial)
			}
			if strings.Contains(result.Partial, "markitdown exploded") {
				t.Fatalf("the converter's own raw error text reached the partial reason: partial=%q", result.Partial)
			}
			if !strings.HasPrefix(result.Content, partialMarkerPrefix) {
				t.Fatalf("the partial flag is not visible in the content: %.200q", result.Content)
			}
			cached := h.Fetch(context.Background(), "https://guide.example.test/birds")
			if cached.CacheStatus != cacheStatusHit || cached.Partial != result.Partial {
				t.Fatalf(
					"the cached artifact lost its partial flag: status=%q partial=%q",
					cached.CacheStatus,
					cached.Partial,
				)
			}
		})
	}
}

// TestRecallGateLeavesAFaithfulExtractionAlone: an article the extractor
// keeps is never flagged, however long.
func TestRecallGateLeavesAFaithfulExtractionAlone(t *testing.T) {
	page := `<html><body><nav>` + strings.Repeat(
		`<a href="/x">Section link</a> `,
		40,
	) + `</nav><article><h1>Essay</h1>` +
		strings.Repeat(
			`<p>A long essay paragraph about estuaries, tides, dunes and the birds that winter there.</p>`,
			40,
		) +
		`</article></body></html>`
	h := pageHarvester(t, page, tagStripConverter(), browserOff())
	result := h.Fetch(context.Background(), "https://essay.example.test/tides")
	if result.Error != "" || result.Partial != "" || strings.HasPrefix(result.Content, partialMarkerPrefix) {
		t.Fatalf("a faithful extraction was flagged: partial=%q error=%q", result.Partial, result.Error)
	}
}

func TestBrowserLazyLoadMarkerFlagsThePartialRender(t *testing.T) {
	render := `<html><head><meta name="harvester-lazy-load" content="incomplete: content was still loading when the ` +
		`time-cap stopped scrolling after 40 rounds" data-harvester-token="` + BrowserMarkerToken() + `">` +
		`</head><body><main>` +
		strings.Repeat(
			`<p>Feed item with a caption about the estuary walk and the birds seen there.</p>`,
			30,
		) +
		`</main></body></html>`
	spy := &browserSpyConverter{html: render, status: http.StatusOK, convertFn: tagStripConverter().Convert}
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://feed.example.test/stream")
	if result.Error != "" || result.Method != "browser-chrome" {
		t.Fatalf("browser render not accepted: method=%q error=%q", result.Method, result.Error)
	}
	if !strings.Contains(result.Partial, "lazy-loaded content incomplete") ||
		!strings.Contains(result.Partial, "time-cap") {
		t.Fatalf("a render the scroll cap cut short was reported complete: partial=%q", result.Partial)
	}
}

// TestPublicResultCarriesThePartialFlag: the public surface strips how an
// artifact was acquired, never whether it is complete.
func TestPublicResultCarriesThePartialFlag(t *testing.T) {
	h := pageHarvester(t, wallFixture(t, "reddit-thread-ssr.html"), &browserSpyConverter{}, browserOff())
	result := h.FetchPublic(
		context.Background(),
		"https://www.reddit.com/r/examplesub/comments/abc123/",
		FetchOptions{Refresh: true},
	)
	if result.Error != "" || !strings.Contains(result.Partial, "3 of 12 comments") {
		t.Fatalf("public result dropped the partial flag: partial=%q error=%q", result.Partial, result.Error)
	}
}

// TestAMarkerNeverPassesForContent: the partial marker is a note ABOUT an
// artifact, never the artifact. An extraction that kept nothing stays empty —
// a local page reports "no usable content" instead of storing the marker as
// its success — and a thin extraction stays thin on the web ladder, where the
// marker's own words must not lift it past the 500-char floor.
func TestAMarkerNeverPassesForContent(t *testing.T) {
	failingFullDOM := func([]byte) (string, error) { return "", errors.New("markitdown exploded") }
	t.Run("empty local extraction", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "guide.html")
		if err := os.WriteFile(path, []byte(catalogPage()), 0o600); err != nil {
			t.Fatal(err)
		}
		empty := legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) { return "", nil })
		h := mustNew(t, Options{
			CacheDir:   t.TempDir(),
			LocalRoots: []string{filepath.Dir(path)},
			Converter:  &fullDOMSpy{Converter: empty, full: failingFullDOM},
		})
		result := h.Fetch(context.Background(), path)
		if result.Error == "" || !strings.Contains(result.Error, "no usable content") {
			t.Fatalf("an empty extraction was stored as a success: content=%q error=%q", result.Content, result.Error)
		}
	})
	t.Run("thin web extraction", func(t *testing.T) {
		thin := legacyConverterFunc(func(_ context.Context, kind, _ string, raw []byte) (string, error) {
			if kind != kindHTML {
				return string(raw), nil
			}
			return "# Field guide to coastal birds\n\n" + strings.Repeat("Recorded on the estuary. ", 15), nil
		})
		h := pageHarvester(t, catalogPage(), &fullDOMSpy{Converter: thin, full: failingFullDOM}, browserOff())
		result := h.Fetch(context.Background(), "https://guide.example.test/birds")
		if result.Error == "" && result.Method == rungDirect {
			t.Fatalf("a thin extraction passed the 500-char floor on its marker: %d chars, partial=%q",
				contentChars(result.Content), result.Partial)
		}
	})
}

// exitErrorForTest runs a tiny subprocess that exits with code and returns
// the *exec.ExitError it produces — the real type errorReasonClass detects,
// not a hand-built stand-in.
func exitErrorForTest(t *testing.T, code int) *exec.ExitError {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("sh did not produce an *exec.ExitError: %v", err)
	}
	return exitErr
}

// TestErrorReasonClassNeverRepeatsRawErrorText: the ONE place a partial
// reason is built from an error (recall.go) never hands back what the error
// itself says — a scratch path, a worker's stderr tail, a raw Go error chain
// — only a short, stable class, or the caller's own fallback for anything it
// does not recognise.
func TestErrorReasonClassNeverRepeatsRawErrorText(t *testing.T) {
	pathBearing := &os.PathError{
		Op:   "open",
		Path: "/tmp/pfm-harvest-fulldom-829172/input.html",
		Err:  errors.New("no such file or directory"),
	}
	stderrBearing := fmt.Errorf(
		"%w (RuntimeError): worker crashed (stderr: Traceback (most recent call last):\n"+
			"  File \"/opt/harvestpy/worker.py\", line 42, in convert\n    raise RuntimeError(\"boom\"))",
		errors.New("harvestpy conversion failed"),
	)
	for _, tc := range []struct {
		name     string
		err      error
		fallback string
		want     string
	}{
		{"nil error", nil, "conversion error", ""},
		{"a path-bearing error", pathBearing, "conversion error", "conversion error"},
		{"a stderr-bearing worker error", stderrBearing, "conversion error", "conversion error"},
		{"a timeout", context.DeadlineExceeded, "conversion error", "timeout"},
		{"a cancellation", context.Canceled, "conversion error", "cancelled"},
		{
			"an HTTP status embedded in the wording",
			fmt.Errorf("DoH query for example.test returned HTTP 429"),
			"conversion error",
			"HTTP 429",
		},
		{"a worker exit code", exitErrorForTest(t, 3), "conversion error", "worker exited 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := errorReasonClass(tc.err, tc.fallback)
			if got != tc.want {
				t.Fatalf("errorReasonClass(%v, %q) = %q, want %q", tc.err, tc.fallback, got, tc.want)
			}
			if strings.Contains(got, "/") {
				t.Fatalf("the class itself carries a path segment: %q", got)
			}
		})
	}
}

// TestFetchPublicNeverRepeatsAScratchPathFromAPartialReason: a full-DOM
// conversion failure whose own wording names a scratch-file path and a
// worker's stderr tail — the shape harvestmcp's convertScratch produces
// under $TMPDIR when the harvestpy worker fails — must never reach the
// public result: not its Partial header, not the cached artifact a public
// caller reads. Watched FAILING on HEAD before the fix (see the red log).
func TestFetchPublicNeverRepeatsAScratchPathFromAPartialReason(t *testing.T) {
	const scratchDir = "/private/tmp/pfm-harvest-fulldom-829172"
	scratchErr := fmt.Errorf(
		"remove full-DOM conversion scratch: RemoveAll %s: harvestpy conversion failed "+
			"(RuntimeError): worker crashed (stderr: Traceback (most recent call last):\n"+
			"  File \"%s/worker.py\", line 42, in convert\n    raise RuntimeError(\"boom\"))",
		scratchDir, scratchDir,
	)
	spy := &fullDOMSpy{Converter: leadOnlyConverter(), full: func([]byte) (string, error) { return "", scratchErr }}
	h := pageHarvester(t, catalogPage(), spy, browserOff())
	source := "https://guide.example.test/birds"

	result := h.FetchPublic(context.Background(), source, FetchOptions{Refresh: true})
	if result.Error != "" {
		t.Fatalf("public fetch failed: %q", result.Error)
	}
	if !strings.Contains(result.Partial, "the full-DOM conversion failed") {
		t.Fatalf("the public result lost the partial flag: partial=%q", result.Partial)
	}
	for _, leaked := range []string{scratchDir, "pfm-harvest-fulldom", "Traceback", "worker.py", "RuntimeError", "RemoveAll"} {
		if strings.Contains(result.Partial, leaked) {
			t.Fatalf("the public PARTIAL reason repeats raw error text (%q): %q", leaked, result.Partial)
		}
	}
	if result.Path == "" {
		t.Fatalf("public fetch produced no cached artifact to check")
	}
	body, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read the public artifact: %v", err)
	}
	for _, leaked := range []string{scratchDir, "pfm-harvest-fulldom", "Traceback", "worker.py", "RuntimeError", "RemoveAll"} {
		if strings.Contains(string(body), leaked) {
			t.Fatalf("the public artifact repeats raw error text (%q): %.400q", leaked, string(body))
		}
	}
}

// TestAFullDOMConversionOfPageChromeIsNamedNotStoredAsComplete: the recall
// gate's full-DOM fallback is measured against the page's visible words, which
// exclude its chrome. A conversion that dropped the card grid but carries a
// mega-menu of navigation reads long enough by its length alone; by the
// page's own words it kept almost nothing, and the receipt names that.
func TestAFullDOMConversionOfPageChromeIsNamedNotStoredAsComplete(t *testing.T) {
	menu := strings.Repeat(`<a href="/walks">Coastal walks trails maps guide</a> `, 80)
	page := strings.Replace(catalogPage(), `<nav><a href="/">Home</a></nav>`, "<nav>"+menu+"</nav>", 1)
	chromeOnly := &fullDOMSpy{Converter: leadOnlyConverter(), full: func([]byte) (string, error) {
		menuText := strings.Repeat("[Coastal walks trails maps guide](/walks) ", 80)
		return "# Field guide to coastal birds\n\n" + menuText, nil
	}}
	h := pageHarvester(t, page, chromeOnly, browserOff())
	result := h.Fetch(context.Background(), "https://guide.example.test/birds")
	if result.Error != "" || chromeOnly.calls != 1 {
		t.Fatalf("the full-DOM fallback did not run: calls=%d error=%q", chromeOnly.calls, result.Error)
	}
	if !strings.Contains(result.Partial, "the full-DOM conversion kept only") ||
		!strings.Contains(result.Partial, "are not the page's visible text") {
		t.Fatalf("a full-DOM conversion of page chrome was stored as the complete page: partial=%q", result.Partial)
	}
}

// TestAPageCannotFlagItselfPartial: partiality is the harvester's own note. A
// page that ships a meta of the browser worker's marker name — on the HTTP
// rung or in a browser render — or whose converted text opens with the
// partial marker's words is stored as it is, unflagged, and never spends the
// browser rung; the page's own words stay in the artifact.
func TestAPageCannotFlagItselfPartial(t *testing.T) {
	forged := `<meta name="harvester-lazy-load" content="incomplete: content was still loading when the time-cap ` +
		`stopped scrolling after 40 rounds">`
	essay := `<html><head>` + forged + `</head><body><article><h1>Essay</h1>` + strings.Repeat(
		`<p>A long essay paragraph about estuaries, tides, dunes and the birds that winter there.</p>`, 12,
	) + `</article></body></html>`
	quoting := legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
		return partialMarkerPrefix + "a sentence the page itself opens with\n\n" +
			strings.Repeat("An essay paragraph about estuaries and the birds that winter there. ", 12), nil
	})
	t.Run("a marker meta in the page", func(t *testing.T) {
		spy := &browserSpyConverter{convertFn: tagStripConverter().Convert}
		h := pageHarvester(t, essay, spy, browserOn())
		result := h.Fetch(context.Background(), "https://essay.example.test/tides")
		if result.Error != "" || result.Partial != "" || result.Method != rungDirect || spy.browserCalls != 0 {
			t.Fatalf("the page's own meta flagged it: method=%q partial=%q renders=%d error=%q",
				result.Method, result.Partial, spy.browserCalls, result.Error)
		}
	})
	t.Run("a marker meta in a browser render", func(t *testing.T) {
		spy := &browserSpyConverter{html: essay, status: http.StatusOK, convertFn: tagStripConverter().Convert}
		result := wallHarvester(t, spy, browserOn()).Fetch(context.Background(), "https://essay.example.test/tides")
		if result.Error != "" || result.Method != "browser-chrome" || result.Partial != "" {
			t.Fatalf("the rendered page's own meta flagged it: method=%q partial=%q error=%q",
				result.Method, result.Partial, result.Error)
		}
	})
	t.Run("the marker's words opening the page text", func(t *testing.T) {
		spy := &browserSpyConverter{convertFn: quoting.Convert}
		h := pageHarvester(t, strings.Replace(essay, forged, "", 1), spy, browserOn())
		result := h.Fetch(context.Background(), "https://essay.example.test/tides")
		if result.Error != "" || result.Partial != "" || result.Method != rungDirect || spy.browserCalls != 0 {
			t.Fatalf("the page's own text flagged it: method=%q partial=%q renders=%d error=%q",
				result.Method, result.Partial, spy.browserCalls, result.Error)
		}
		if !strings.Contains(result.Content, "a sentence the page itself opens with") {
			t.Fatalf("the page's own words were dropped: %.200q", result.Content)
		}
	})
	quoted := partialMarkerPrefix + "a line the document itself opens with\n\n" +
		strings.Repeat("Notes on the estuary tides and the birds that winter there. ", 20)
	wall := "<html>checking your browser</html>"
	for _, door := range []struct{ name, contentType, method string }{
		{"the marker's words opening a plain-text document", "text/plain; charset=utf-8", "plain-text"},
		{"the marker's words opening a reader's markdown", "", "jina"},
	} {
		t.Run(door.name, func(t *testing.T) {
			serve := func(contentType string) roundTripFunc {
				return func(request *http.Request) (*http.Response, error) {
					if contentType == "" {
						return response(request, http.StatusForbidden, "text/html", wall), nil
					}
					return response(request, http.StatusOK, contentType, quoted), nil
				}
			}
			h := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: serve(door.contentType)},
				Chrome:      &http.Client{Transport: serve(door.contentType)},
				Jina:        &http.Client{Transport: serve("text/plain")},
				OA:          &http.Client{Transport: serve("")},
				Converter:   &browserSpyConverter{},
				BrowserRung: browserOff(),
			})
			result := h.Fetch(context.Background(), "https://essay.example.test/notes")
			if result.Error != "" || result.Partial != "" || result.Method != door.method ||
				!strings.Contains(result.Content, "a line the document itself opens with") {
				t.Fatalf("the document's own text flagged it: method=%q partial=%q error=%q",
					result.Method, result.Partial, result.Error)
			}
		})
	}
}

// githubDiscussionComment is one comment of a GitHub Discussions timeline, cut
// from the real page's markup (usernames scrubbed): the author header, the
// hidden "Uh oh!" error slate GitHub ships with every comment, and the body in
// a role="presentation" layout table, with a link mid-paragraph.
const githubDiscussionComment = `<div class="js-timeline-item js-timeline-progressive-focus-container">` +
	`<div class="timeline-comment-group"><h3 class="f5 text-normal">` +
	`<a class="Link--primary text-bold" href="/user-%02[1]d">user-%02[1]d</a> ` +
	`<relative-time datetime="2022-10-25T19:28:22Z" class="no-wrap">Oct 25, 2022</relative-time></h3>` +
	`<a href="/user-%02[1]d">user-%02[1]d</a> <a href="#discussioncomment-%[1]d">Oct 25, 2022</a>` +
	`<div data-show-on-forbidden-error hidden><div class="Box"><div class="blankslate-container">` +
	`<h3 class="blankslate-heading">Uh oh!</h3><p>There was an error while loading. Please reload this page.</p>` +
	`</div></div></div>` +
	`<div class="edit-comment-hide"><task-lists disabled sortable>` +
	`<table class="d-block" role="presentation" data-paste-markdown-skip><tr class="d-block">` +
	`<td class="d-block comment-body markdown-body js-comment-body">` +
	`<p>%[2]s (currently using <a href="https://runtime.example.test/intro" rel="nofollow">a runtime</a> %[3]s ` +
	`closing-remark-%02[1]d.</p><p>%[4]s</p></td></tr></table></task-lists></div></div></div>`

// githubDiscussionPage is a discussion of n comments; lead is each body's text
// before its link — all a main-content extractor keeps of it.
func githubDiscussionPage(n int) (page, lead string) {
	lead = strings.TrimSpace(strings.Repeat("the form posts are handled by progressive enhancement on the server ", 2))
	after := strings.TrimSpace(
		strings.Repeat("to get a remix like experience which we hoped would be front and center ", 3),
	)
	more := strings.TrimSpace(
		strings.Repeat("server actions and mutations still need a documented story for forms ", 3),
	)
	var b strings.Builder
	b.WriteString(`<html><head><title>[Feedback] Router Beta · Discussion #1</title></head><body>` +
		`<nav><a href="/">Home</a></nav><main><h1>[Feedback] Router Beta #1</h1>` +
		`<h2>Replies: 30 comments</h2>`)
	for index := 1; index <= n; index++ {
		fmt.Fprintf(&b, githubDiscussionComment, index, lead, after, more)
	}
	b.WriteString(`</main><footer>Footer</footer></body></html>`)
	return b.String(), lead
}

// timelineHeadersConverter is what the main-content extractor made of the
// real discussion: every comment's header, its hidden "Uh oh!" slate and the
// body cut at its first link — the words it kept that the reader never sees
// (the hidden slate) standing in for the bodies it dropped.
func timelineHeadersConverter(n int, lead string) Converter {
	return legacyConverterFunc(func(_ context.Context, kind, _ string, raw []byte) (string, error) {
		if kind != kindHTML {
			return string(raw), nil
		}
		var b strings.Builder
		b.WriteString("# [Feedback] Router Beta #1\n\n## Replies: 30 comments\n\n")
		for index := 1; index <= n; index++ {
			fmt.Fprintf(&b, "### Uh oh!\n\nThere was an error while loading. Please reload this page.\n\n"+
				"### [user-%02[1]d](/user-%02[1]d) Oct 25, 2022\n\n[user-%02[1]d](/user-%02[1]d)\n\n"+
				"[Oct 25, 2022](#discussioncomment-%[1]d)\n\n|  |\n|---|\n| %[2]s (currently using |\n\n", index, lead)
		}
		return b.String(), nil
	})
}

// TestRecallGateCountsOnlyVisibleWordsOfAMainContentExtraction: a GitHub
// Discussions page whose extraction kept each comment's header and first
// fragment falls back to the full DOM, so every comment body survives. Words
// the extraction kept that the reader never sees (a hidden error slate) do not
// count toward its recall.
func TestRecallGateCountsOnlyVisibleWordsOfAMainContentExtraction(t *testing.T) {
	const comments = 30
	page, lead := githubDiscussionPage(comments)
	spy := &fullDOMSpy{Converter: timelineHeadersConverter(comments, lead), full: func(body []byte) (string, error) {
		return tagStripConverter().Convert(context.Background(), kindHTML, "", body)
	}}
	h := pageHarvester(t, page, spy, browserOff())
	result := h.Fetch(context.Background(), "https://github.com/example-org/example-repo/discussions/1")
	if result.Error != "" || spy.calls != 1 {
		t.Fatalf("a comment timeline cut to its headers was stored as the page: calls=%d error=%q partial=%q",
			spy.calls, result.Error, result.Partial)
	}
	for index := 1; index <= comments; index++ {
		if want := fmt.Sprintf("closing-remark-%02d", index); !strings.Contains(result.Content, want) {
			t.Fatalf("comment %d's body is missing from the stored page (%s): %.400q", index, want, result.Content)
		}
	}
}
