package harvest

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Fixtures under testdata/walls are hand-built from the structure of live
// pages captured in the real-simulation fence; every username, id, post and
// comment text in them is an invented placeholder.
//
//   - reddit-thread-rendered.html: a thread as the browser rung serves it
//     after hydration — a text <shreddit-post>, a nested <shreddit-comment>
//     tree (depth 0-3) with one deleted and one removed stub, one "3 more
//     replies" loader and one "View more comments" loader, ads and chrome.
//     States 12 comments; 8 are in the page.
//   - reddit-thread-ssr.html: the same thread as a plain GET serves it, the
//     first page of comments streamed inside an inert <template>. 3 of 12.
//   - deferred-article.html: a non-forum documentation page whose article
//     body is streamed inside a <template> for hydration.
func wallFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "walls", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(body)
}

var (
	templateBlockRe = regexp.MustCompile(`(?is)<template[^>]*>.*?</template>`)
	noscriptBlockRe = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
)

// inertSkippingConverter stands in for trafilatura on the one behaviour these
// tests are about: a main-content extractor skips inert containers by spec
// (a <template>'s content and a <noscript>'s body are never page content).
func inertSkippingConverter() Converter {
	return legacyConverterFunc(func(_ context.Context, kind, _ string, raw []byte) (string, error) {
		if kind != kindHTML {
			return string(raw), nil
		}
		text := templateBlockRe.ReplaceAllString(string(raw), " ")
		text = noscriptBlockRe.ReplaceAllString(text, " ")
		text = scriptRe.ReplaceAllString(text, " ")
		text = styleRe.ReplaceAllString(text, " ")
		text = htmlTagRe.ReplaceAllString(text, " ")
		return strings.Join(strings.Fields(text), " "), nil
	})
}

// pageHarvester serves page at every direct/chrome request (a sibling path
// that cannot exist is a 404, as on a real server-rendered site, and so is a
// Reddit loader or "Continue this thread" request: the fixture page holds no
// answer to one) and answers every reader rung with a 404.
func pageHarvester(t *testing.T, page string, converter Converter, browserRung *bool) *Harvester {
	t.Helper()
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, appShellProbePrefix) ||
			strings.HasPrefix(request.URL.Path, "/svc/") || strings.Contains(request.URL.Path, "/comment/") {
			return response(
				request,
				http.StatusNotFound,
				"text/html",
				"<html><body><h1>Page not found</h1></body></html>",
			), nil
		}
		return response(request, http.StatusOK, "text/html", page), nil
	})
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: site},
		Chrome:      &http.Client{Transport: site},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: missing},
		Converter:   converter,
		BrowserRung: browserRung,
		Clock:       newPacingClock(),
	})
}

// TestDeferredTemplateContentReachesTheConverter: a documentation page (no
// forum markup) streams its article inside a <template> for hydration. The
// extractor skips inert content by spec, so without the pre-pass the page
// converts to its teaser and the article is lost.
func TestDeferredTemplateContentReachesTheConverter(t *testing.T) {
	h := pageHarvester(t, wallFixture(t, "deferred-article.html"), inertSkippingConverter(), browserOff())
	result := h.Fetch(context.Background(), "https://docs.example.test/scheduler/")
	if result.Error != "" || result.Method != rungDirect {
		t.Fatalf("deferred article not fetched at the direct rung: method=%q rungs=%v error=%q",
			result.Method, result.Rungs, result.Error)
	}
	for _, want := range []string{"dead-letter queue", "starvation window", "metrics endpoint"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("deferred article content %q missing from the artifact: %.400q", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "Copy to clipboard") {
		t.Fatalf("a UI-stub template in <head> was surfaced as content: %.400q", result.Content)
	}
}

// TestInertUnwrapSurfacesOnlyUnseenDeferredContent pins the pre-pass rules on
// one page: a UI-stub template stays inert, a <noscript> repeating what the
// page already shows stays inert, a declarative shadow root is always
// rendered, deferred content is surfaced once, and a JSON-LD articleBody the
// page does not show joins the body.
func TestInertUnwrapSurfacesOnlyUnseenDeferredContent(t *testing.T) {
	visible := strings.Repeat("the rendered story names the harbour, the ferry and the lighthouse keeper ", 4)
	deferred := strings.Repeat("a deferred chapter about tides, charts and night crossings of the bay ", 4)
	island := strings.Repeat("an island only chapter about storms, beacons and rescue boats on the coast ", 4)
	page := `<html><head><script type="application/ld+json">{"@type":"Article","articleBody":"` + island + `"}</script>` +
		`<script type="application/ld+json">{not json</script></head><body>` +
		`<article><p>` + visible + `</p></article>` +
		`<template id="stub"><span>Copy link</span></template>` +
		`<noscript><p>` + visible + `</p></noscript>` +
		`<div><template shadowrootmode="open"><p>shadow text</p></template></div>` +
		`<template id="deferred"><section><p>` + deferred + `</p></section></template>` +
		`</body></html>`
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	if got := unwrapInertContainers(context.Background(), doc); got != 3 {
		t.Fatalf("unwrapInertContainers surfaced %d containers, want 3 (shadow root, deferred template, JSON-LD)", got)
	}
	var rendered bytes.Buffer
	if err := html.Render(&rendered, doc); err != nil {
		t.Fatal(err)
	}
	out := rendered.String()
	for _, want := range []string{
		`<div data-harvest-unwrapped="template"><section><p>a deferred chapter`,
		`<div data-harvest-unwrapped="template"><p>shadow text</p>`,
		`<section data-harvest-unwrapped="json-ld"><p>an island only chapter`,
		`<template id="stub">`,
		`<noscript>`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("unwrapped page lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "the rendered story") != 8 {
		t.Fatalf(
			"the <noscript> duplicate of visible content was surfaced: %d copies",
			strings.Count(out, "the rendered story"),
		)
	}
}

// TestInertBookkeepingGrowsLinearly: every surfaced container's words join
// the shown text the next probe searches, so the bookkeeping must grow with
// the words surfaced — a page of 2N distinct deferred containers may cost at
// most about twice the bytes of a page of N, never the square.
func TestInertBookkeepingGrowsLinearly(t *testing.T) {
	page := func(containers int) string {
		var b strings.Builder
		b.WriteString(`<html><body><p>The visible lede of the page.</p>`)
		for index := 0; index < containers; index++ {
			b.WriteString(`<template><p>`)
			for word := 0; word < 40; word++ {
				fmt.Fprintf(&b, "c%dw%d ", index, word)
			}
			b.WriteString(`</p></template>`)
		}
		b.WriteString(`</body></html>`)
		return b.String()
	}
	allocated := func(containers int) int64 {
		markup := page(containers)
		doc, err := html.Parse(strings.NewReader(markup))
		if err != nil {
			t.Fatal(err)
		}
		if got := unwrapInertContainers(context.Background(), doc); got != containers {
			t.Fatalf("surfaced %d of %d distinct unseen containers", got, containers)
		}
		result := testing.Benchmark(func(b *testing.B) {
			for range b.N {
				b.StopTimer()
				doc, err := html.Parse(strings.NewReader(markup))
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				unwrapInertContainers(context.Background(), doc)
			}
		})
		if result.N == 0 {
			t.Fatalf("the allocation benchmark over %d containers did not run or failed", containers)
		}
		return result.AllocedBytesPerOp()
	}
	const n = 400
	single, double := allocated(n), allocated(2*n)
	if double >= 3*single {
		t.Fatalf("the pre-pass allocated %d bytes over %d containers and %d over %d: more than 3x, not linear",
			single, n, double, 2*n)
	}
}

// TestANestedDeferredTemplateSurfacesWithItsParent: a deferred comment tree
// streamed in a <template> holds its deferred replies in a nested <template>.
// Surfacing the parent counts the nested replies as shown, so the nested
// container must surface too — never stay inert, dropped by the extractor and
// unseen by the recall gate.
func TestANestedDeferredTemplateSurfacesWithItsParent(t *testing.T) {
	words := func(prefix string) string {
		var b strings.Builder
		for word := 0; word < 40; word++ {
			fmt.Fprintf(&b, "%s%d ", prefix, word)
		}
		return b.String()
	}
	markup := `<html><body><p>The visible lede of the page.</p><template><div><p>` + words("tree") +
		`</p><template><p>` + words("reply") + `</p></template></div></template>` +
		`<template><p>a short tooltip stub</p></template></body></html>`
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	if got := unwrapInertContainers(context.Background(), doc); got != 2 {
		t.Fatalf("surfaced %d containers, want the tree and its nested replies (2)", got)
	}
	shown := strings.Join(visibleWords(doc), " ")
	for _, want := range []string{"tree0 tree1", "reply0 reply1"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("%q is still inert after the pre-pass; visible: %.300q", want, shown)
		}
	}
	if strings.Contains(shown, "tooltip") {
		t.Fatalf("a short UI stub template was surfaced: %.300q", shown)
	}
}
