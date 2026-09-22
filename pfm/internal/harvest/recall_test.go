package harvest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
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
		}, "the full-DOM conversion failed: markitdown exploded"},
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
	render := `<html><head><meta name="harvester-lazy-load" content="incomplete: content was still loading when the time-cap stopped scrolling after 40 rounds"></head><body><main>` +
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
