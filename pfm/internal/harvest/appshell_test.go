package harvest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appShellFixture reproduces the shape of a live client-rendered site (a Vite
// + React app behind Cloudflare): every route serves this SAME document — a
// module bundle, a #root mount point, and a no-JS explainer long enough to
// clear the 500-char thin-page floor. The route's real content exists only
// after the bundle runs.
func appShellFixture() string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><title>Example Club: meetups and hackdays</title>`)
	b.WriteString(
		`<style>#no-js-home { display: none; }</style><noscript><style>#no-js-home { display: block; }</style></noscript>`,
	)
	b.WriteString(`<script type="module" crossorigin src="/assets/index-Abc123.js"></script></head><body>`)
	b.WriteString(`<div id="root"><main id="no-js-home"><h1>Example Club</h1>`)
	for i := 0; i < 6; i++ {
		b.WriteString(
			`<p>Example Club is the web application that runs a volunteer community. Organizers and members use it to plan and run meetups, hackdays and conferences across several cities, and keep one shared member and partner directory.</p>`,
		)
	}
	b.WriteString(`</main></div></body></html>`)
	return b.String()
}

// renderedEventsMarkdown is what a JS-rendering reader returns for /events:
// the route's own content, which the static shell never contained.
const renderedEventsMarkdown = "Title: Events · Example Club\n\nURL Source: https://club.example.test/events\n\nMarkdown Content:\n# Events\n\nUpcoming events · 3 upcoming\n\n[Wed 16 Sep — Example Club Amsterdam · Meetup · 5 hosts · 13 speakers](https://club.example.test/e/AMS-Sep16)\n\n[Mon 21 Sep — Example Club Barcelona · Summit Edition · 3 speakers](https://club.example.test/e/BCN-Sep21)\n\n[Tue 22 Sep — Example Club Berlin · Meetup · 3 speakers](https://club.example.test/e/BER-Sep22)\n"

// tagStripConverter stands in for trafilatura: it drops script/style/noscript
// bodies and tags, the way a real HTML extractor reduces the shell to its
// explainer prose.
func tagStripConverter() Converter {
	return legacyConverterFunc(func(_ context.Context, kind, _ string, raw []byte) (string, error) {
		if kind != "html" {
			return string(raw), nil
		}
		text := scriptRe.ReplaceAllString(string(raw), " ")
		text = styleRe.ReplaceAllString(text, " ")
		text = htmlTagRe.ReplaceAllString(text, " ")
		return strings.Join(strings.Fields(text), " "), nil
	})
}

// shellSite serves the app shell for EVERY path on the app host (the SPA
// catch-all) and answers defuddle.md with the shell's own prose, the way a
// non-rendering reader does. Jina gets its own transport per test.
func shellSite(t *testing.T, jina http.RoundTripper, browser *browserSpyConverter) *Harvester {
	t.Helper()
	shell := appShellFixture()
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "defuddle.md" {
			prose := "---\ntitle: Example Club\n---\n" + strings.Repeat(
				"Example Club is the web application that runs a volunteer community. Organizers and members use it to plan and run meetups, hackdays and conferences across several cities. ",
				6,
			)
			return response(request, http.StatusOK, "text/markdown", prose), nil
		}
		return response(request, http.StatusOK, "text/html", shell), nil
	})
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	if jina == nil {
		jina = missing
	}
	converter := tagStripConverter()
	rung := browserOff()
	if browser != nil {
		browser.convertFn = tagStripConverter().Convert
		converter = browser
		rung = browserOn()
	}
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: site},
		Chrome:      &http.Client{Transport: site},
		Jina:        &http.Client{Transport: jina},
		OA:          &http.Client{Transport: missing},
		Converter:   converter,
		BrowserRung: rung,
	})
}

func cachedArtifacts(t *testing.T, root string) []string {
	t.Helper()
	var artifacts []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && filepath.Base(path) != "stats.jsonl" {
			artifacts = append(artifacts, path)
		}
		return nil
	})
	return artifacts
}

// TestAppShellEscalatesToRenderingReader pins the live defect seen on a
// Vite + React community site: a client-rendered route whose static shell clears the thin-page
// floor was stored as the page itself at the direct rung. The shell must
// escalate, and the rendering reader's route content must win.
func TestAppShellEscalatesToRenderingReader(t *testing.T) {
	jina := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/plain", renderedEventsMarkdown), nil
	})
	h := shellSite(t, jina, nil)
	result := h.Fetch(context.Background(), "https://club.example.test/events")
	if result.Error != "" {
		t.Fatalf("app shell route failed instead of escalating to the rendering reader: %#v", result)
	}
	if result.Method != "jina" {
		t.Fatalf(
			"app shell accepted at rung %q instead of escalating to jina: rungs=%v content=%.200q",
			result.Method,
			result.Rungs,
			result.Content,
		)
	}
	if !strings.Contains(result.Content, "Amsterdam") {
		t.Fatalf("rendered route content missing: %.300q", result.Content)
	}
}

// TestAppShellNeverLaunderedThroughNonRenderingReader pins the second door:
// defuddle.md re-serves the shell's prose with frontmatter padding, which
// is LONGER than what the direct rung extracted — length must not launder a
// shell into content. With no renderer available, the failure names the app
// shell and the enable path, and nothing is cached.
func TestAppShellNeverLaunderedThroughNonRenderingReader(t *testing.T) {
	h := shellSite(t, nil, nil)
	result := h.Fetch(context.Background(), "https://club.example.test/events")
	if result.Error == "" {
		t.Fatalf(
			"app shell returned as SUCCESS (method %q) with no renderer available: %.200q",
			result.Method,
			result.Content,
		)
	}
	if result.ErrorKind != "app_shell" {
		t.Fatalf("ErrorKind=%q, want app_shell: %q", result.ErrorKind, result.Error)
	}
	for _, want := range []string{"JavaScript app shell", "fetch.browser=true"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("terminal error missing %q: %q", want, result.Error)
		}
	}
	if artifacts := cachedArtifacts(t, h.options.CacheDir); len(artifacts) != 0 {
		t.Fatalf("app shell entered the positive cache: %#v", artifacts)
	}
}

// TestAppShellBrowserRenderWins pins the browser rung's door: with jina
// missing, a real-browser render carrying the route's content is accepted.
func TestAppShellBrowserRenderWins(t *testing.T) {
	rendered := `<html><body><div id="root"><h1>Events</h1>` + strings.Repeat(
		`<p>Wed 16 Sep — Example Club Amsterdam meetup with 5 hosts and 13 speakers on agent evaluation and retrieval pipelines.</p>`,
		6,
	) + `</div></body></html>`
	spy := &browserSpyConverter{html: rendered, status: http.StatusOK}
	h := shellSite(t, nil, spy)
	result := h.Fetch(context.Background(), "https://club.example.test/events")
	if result.Error != "" || result.Method != "browser-chrome" {
		t.Fatalf("browser render of the app route not accepted: method=%q err=%q", result.Method, result.Error)
	}
	if spy.browserCalls != 1 {
		t.Fatalf("browser rung consulted %d times, want 1", spy.browserCalls)
	}
}

// TestAppShellBrowserRenderOfShellIsRejected: a render that still shows only
// the shell (the bundle failed to boot) is not the route's content.
func TestAppShellBrowserRenderOfShellIsRejected(t *testing.T) {
	spy := &browserSpyConverter{html: appShellFixture(), status: http.StatusOK}
	h := shellSite(t, nil, spy)
	result := h.Fetch(context.Background(), "https://club.example.test/events")
	if result.Error == "" {
		t.Fatalf("browser render of the bare shell accepted as content (method %q)", result.Method)
	}
	if result.ErrorKind != "app_shell" || !strings.Contains(result.Error, "rendered only the shell") {
		t.Fatalf("shell-only render not named: kind=%q err=%q", result.ErrorKind, result.Error)
	}
	// A shell is not a wall: the rung renders it once.
	if len(spy.sources) != 1 {
		t.Fatalf("render requests %v, want exactly one", spy.sources)
	}
}

// TestServerRenderedPageWithBundleIsNotAShell guards the false positive: an
// SSR page that also ships a module bundle and a #root mount serves a
// DIFFERENT document for a nonexistent sibling, so it is accepted directly.
func TestServerRenderedPageWithBundleIsNotAShell(t *testing.T) {
	page := `<html><head><script type="module" src="/assets/app.js"></script></head><body><div id="root"><h1>Events</h1>` +
		strings.Repeat(
			`<p>Wed 16 Sep — Amsterdam meetup, server-rendered with every talk listed in the markup itself.</p>`,
			8,
		) + `</div></body></html>`
	notFound := `<html><head><script type="module" src="/assets/app.js"></script></head><body><div id="root"><h1>Page not found</h1></div></body></html>`
	var probes int
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/events" {
			return response(request, http.StatusOK, "text/html", page), nil
		}
		probes++
		return response(request, http.StatusNotFound, "text/html", notFound), nil
	})
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("an accepted SSR page must not reach %s", request.URL)
		return nil, nil
	})
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: site},
		Chrome:      &http.Client{Transport: site},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: missing},
		Converter:   tagStripConverter(),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), "https://ssr.example.test/events")
	if result.Error != "" || result.Method != "direct" {
		t.Fatalf("SSR page not accepted directly: method=%q err=%q", result.Method, result.Error)
	}
	if probes != 1 {
		t.Fatalf("sibling probe ran %d times, want exactly 1", probes)
	}
}

// TestPlainPageIsNeverProbed: a page with no client-app markers never pays
// for the sibling probe.
func TestPlainPageIsNeverProbed(t *testing.T) {
	page := `<html><body><article><h1>Notes</h1>` + strings.Repeat(
		`<p>A plain static article with enough prose to clear the thin-page floor on its own.</p>`,
		10,
	) + `</article></body></html>`
	var requests int
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return response(request, http.StatusOK, "text/html", page), nil
	})
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: site},
		Chrome:      &http.Client{Transport: site},
		Jina:        &http.Client{Transport: site},
		OA:          &http.Client{Transport: site},
		Converter:   tagStripConverter(),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), "https://static.example.test/notes")
	if result.Error != "" || result.Method != "direct" {
		t.Fatalf("plain page not accepted directly: method=%q err=%q", result.Method, result.Error)
	}
	if requests != 1 {
		t.Fatalf("plain page cost %d requests, want 1 (no probe)", requests)
	}
}

func TestAppShellProbeURLIsASibling(t *testing.T) {
	for _, tc := range []struct{ source, wantPrefix string }{
		{"https://club.example.test/events", "https://club.example.test/"},
		{"https://club.example.test/", "https://club.example.test/"},
		{"https://club.example.test/docs/intro?x=1#top", "https://club.example.test/docs/"},
		{"https://club.example.test/docs/", "https://club.example.test/docs/"},
	} {
		got, ok := appShellProbeURL(tc.source)
		if !ok || !strings.HasPrefix(got, tc.wantPrefix+appShellProbePrefix) || strings.ContainsAny(got, "?#") {
			t.Fatalf("appShellProbeURL(%q)=%q ok=%v, want sibling under %q", tc.source, got, ok, tc.wantPrefix)
		}
	}
}

// TestAppShellWaybackSnapshotOfShellIsRejected pins the Wayback door: a
// snapshot of a client-rendered route is the same shell, and the snapshot's
// own host is NOT a catch-all (its sibling probe 404s), so only the shell text
// carried into the recursion stops it being stored and returned as content.
func TestAppShellWaybackSnapshotOfShellIsRejected(t *testing.T) {
	shell := appShellFixture()
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "web.archive.org" && strings.Contains(request.URL.Path, appShellProbePrefix) {
			return response(
				request,
				http.StatusNotFound,
				"text/html",
				"<html><body>Wayback has not archived that URL.</body></html>",
			), nil
		}
		return response(request, http.StatusOK, "text/html", shell), nil
	})
	archive := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "archive.org" && request.URL.Path == "/wayback/available" {
			return jsonResponse(
				request,
				`{"archived_snapshots":{"closest":{"available":true,"timestamp":"20260901000000"}}}`,
			), nil
		}
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: site},
		Chrome:      &http.Client{Transport: site},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: archive},
		Converter:   tagStripConverter(),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), "https://club.example.test/events")
	if result.Error == "" {
		t.Fatalf(
			"Wayback snapshot of the app shell returned as SUCCESS (method %q, rungs %v)",
			result.Method,
			result.Rungs,
		)
	}
	if result.ErrorKind != "app_shell" {
		t.Fatalf("ErrorKind=%q, want app_shell: %q", result.ErrorKind, result.Error)
	}
	if artifacts := cachedArtifacts(t, h.options.CacheDir); len(artifacts) != 0 {
		t.Fatalf("Wayback snapshot of the shell entered the positive cache: %#v", artifacts)
	}
}

// TestAppShellProbeSendsTheProvenanceReferer: on a Referer-gated host the
// page arrives (the ladder sends the provenance Referer) but a Referer-less
// sibling probe meets the wall, so the probe "could not compare" and the shell
// was stored as the route's content. The probe arrives the way the page did.
func TestAppShellProbeSendsTheProvenanceReferer(t *testing.T) {
	shell := appShellFixture()
	var probeReferer []string
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, appShellProbePrefix) {
			probeReferer = append(probeReferer, request.Header.Get("Referer"))
		}
		if request.Header.Get("Referer") == "" {
			return response(
				request,
				http.StatusForbidden,
				"text/html",
				"<html><h1>Prove your humanity</h1></html>",
			), nil
		}
		return response(request, http.StatusOK, "text/html", shell), nil
	})
	jina := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/plain", renderedEventsMarkdown), nil
	})
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: site},
		Chrome:      &http.Client{Transport: site},
		Jina:        &http.Client{Transport: jina},
		OA:          &http.Client{Transport: missing},
		Converter:   tagStripConverter(),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), "https://club.example.test/events")
	if len(probeReferer) == 0 || probeReferer[0] != ProvenanceReferer {
		t.Fatalf("app-shell probe Referer = %q, want %q", probeReferer, ProvenanceReferer)
	}
	if result.Error != "" || result.Method != "jina" {
		t.Fatalf("shell on a Referer-gated host was not detected: method=%q rungs=%v error=%q",
			result.Method, result.Rungs, result.Error)
	}
}
