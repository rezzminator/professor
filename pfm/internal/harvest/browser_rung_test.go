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

// browserOff explicitly pins the HARVESTER_BROWSER gate OFF so the rung-list
// parity assertions below are deterministic regardless of ambient environment.
func browserOff() *bool {
	value := false
	return &value
}

// browserOn explicitly enables the opt-in real-browser rung for a test.
func browserOn() *bool {
	value := true
	return &value
}

// browserSpyConverter implements Converter AND BrowserFetcher, recording every
// escalation so tests can prove the worker was (or was never) consulted.
type browserSpyConverter struct {
	convertFn    func(ctx context.Context, kind, source string, body []byte) (string, error)
	browserCalls int
	html         string
	status       int
	err          error
	// modes records the headless flag of every call, in order; render, when
	// set, answers per mode instead of html/status/err.
	modes  []bool
	render func(headless bool) (string, int, error)
}

func (spy *browserSpyConverter) Convert(ctx context.Context, kind, source string, body []byte) (string, error) {
	if spy.convertFn != nil {
		return spy.convertFn(ctx, kind, source, body)
	}
	return "", errors.New("spy converter refuses every conversion")
}

func (spy *browserSpyConverter) FetchBrowser(_ context.Context, _ string, headless bool) (string, int, error) {
	spy.browserCalls++
	spy.modes = append(spy.modes, headless)
	if spy.render != nil {
		return spy.render(headless)
	}
	return spy.html, spy.status, spy.err
}

func wallHarvester(t *testing.T, converter Converter, browserRung *bool) *Harvester {
	t.Helper()
	wall := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", "<html>checking your browser</html>"), nil
	})
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: wall},
		Chrome:      &http.Client{Transport: wall},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: missing},
		Converter:   converter,
		BrowserRung: browserRung,
	})
}

// TestBrowserRungSitsBetweenDefuddleAndWayback pins the R6 placement: with the
// gate ON and every earlier rung failing, the trace reads direct,
// chrome-impersonation, jina, defuddle, browser, wayback.
func TestBrowserRungSitsBetweenDefuddleAndWayback(t *testing.T) {
	spy := &browserSpyConverter{err: errors.New("browser unavailable")}
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	want := "direct,chrome-impersonation,jina,defuddle,browser,wayback"
	if strings.Join(result.Rungs, ",") != want {
		t.Fatalf("rungs=%v, want %s", result.Rungs, want)
	}
	if spy.browserCalls == 0 {
		t.Fatal("browser rung was enabled but the BrowserFetcher was never consulted")
	}
}

// TestBrowserRungIsOffByDefault pins R5: with fetch.browser off the rung
// label never appears and the worker is never started — and the retired
// HARVESTER_BROWSER variable can no longer switch it on.
func TestBrowserRungIsOffByDefault(t *testing.T) {
	t.Setenv("HARVESTER_BROWSER", "1")
	spy := &browserSpyConverter{err: errors.New("browser must not be consulted")}
	h := wallHarvester(t, spy, nil)
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	for _, rung := range result.Rungs {
		if rung == "browser" {
			t.Fatalf("browser rung appeared while disabled: %v", result.Rungs)
		}
	}
	if spy.browserCalls != 0 {
		t.Fatalf("browser worker started %d time(s) while the rung is disabled", spy.browserCalls)
	}
}

// TestBrowserOutageIsNotAbsence pins T6's distinction: an ENABLED rung that
// could not run (patchright missing) is reported as an outage and NEVER as
// "the browser tried and found nothing".
func TestBrowserOutageIsNotAbsence(t *testing.T) {
	spy := &browserSpyConverter{err: errors.New("patchright not installed")}
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if !strings.Contains(result.Error, "could NOT RUN") || !strings.Contains(result.Error, "patchright not installed") {
		t.Fatalf("outage not named in terminal error: %q", result.Error)
	}
	if strings.Contains(result.Error, "DID run") {
		t.Fatalf("an outage was misreported as a real attempt: %q", result.Error)
	}
}

// TestDisabledBrowserRungNamesEnablePath pins the absence branch: when the
// ladder dead-ends on a challenge with the gate off, the terminal message
// names the enable path.
func TestDisabledBrowserRungNamesEnablePath(t *testing.T) {
	spy := &browserSpyConverter{err: errors.New("must never run")}
	h := wallHarvester(t, spy, nil)
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if !strings.Contains(result.Error, "DISABLED") ||
		!strings.Contains(result.Error, "fetch.browser=true in harvester.config.json") {
		t.Fatalf("disabled state not named with enable path: %q", result.Error)
	}
}

// TestBrowserChallengePageIsNeverLaunderedIntoContent pins test 6: a browser
// render that returns a LONGER Cloudflare challenge page is rejected — the
// cache stays free of content artifacts.
func TestBrowserChallengePageIsNeverLaunderedIntoContent(t *testing.T) {
	blockPage := cloudflareBlockPageFixture()
	if len(blockPage) <= 4000 {
		t.Fatalf(
			"fixture must reproduce the live page's bulk (its h1 is a STRONG marker matched at any length; the length is what proves a longer-than-earlier-rungs challenge cannot slip through as content), got %d bytes",
			len(blockPage),
		)
	}
	spy := &browserSpyConverter{
		html:   blockPage,
		status: http.StatusForbidden,
		convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
			if strings.Contains(string(body), "Sorry, you have been blocked") {
				return strings.Repeat("laundered text ", 2000), nil
			}
			return "thin wall text", nil
		},
	}
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if result.Error == "" || result.Path != "" {
		t.Fatalf("challenge page stored as content: %#v", result)
	}
	var artifacts []string
	_ = filepath.Walk(h.options.CacheDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && filepath.Base(path) != "stats.jsonl" {
			artifacts = append(artifacts, path)
		}
		return nil
	})
	if len(artifacts) != 0 {
		t.Fatalf("challenge page entered cache as content: %#v", artifacts)
	}
}

// TestBrowserSuccessStoresAcceptedContent covers the happy acceptance path:
// rendered HTML that converts longer than everything before it wins at the
// browser rung and is cached under method browser-chrome.
func TestBrowserSuccessStoresAcceptedContent(t *testing.T) {
	rendered := "<html><body><h1>Recovered article</h1>" + strings.Repeat(
		"real rendered evidence ",
		100,
	) + "</body></html>"
	spy := &browserSpyConverter{
		html:   rendered,
		status: http.StatusOK,
		convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
			if strings.Contains(string(body), "Recovered article") {
				return "# Recovered article\n\n" + strings.Repeat("real rendered evidence ", 100), nil
			}
			return "thin wall text", nil
		},
	}
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://blocked.example.test/recovered-via-browser")
	if result.Error != "" || result.Method != "browser-chrome" {
		t.Fatalf("browser success not stored: %#v", result)
	}
	found := false
	for _, rung := range result.Rungs {
		if rung == "browser" {
			found = true
		}
	}
	if !found {
		t.Fatalf("success receipt lost the browser rung: %v", result.Rungs)
	}
}

// nonChallengeTransport answers every request with a plain 404 — a failure
// that carries NO challenge marker, so any Challenge flag must come from the
// browser rung itself.
func nonChallengeTransport() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})}
}

func browserHarvester(t *testing.T, converter Converter) *Harvester {
	t.Helper()
	missing := nonChallengeTransport()
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      missing,
		Chrome:      nonChallengeTransport(),
		Jina:        missing,
		OA:          nonChallengeTransport(),
		Converter:   converter,
		BrowserRung: browserOn(),
	})
}

// TestBrowserDetectedChallengeIsReported pins review finding 7: when ONLY the
// browser rung identifies the wall (earlier rungs failed with no challenge
// pattern), Result.Challenge is set and the terminal message says the rung
// ran — the flag must not depend on rung one having seen the wall first.
func TestBrowserDetectedChallengeIsReported(t *testing.T) {
	spy := &browserSpyConverter{
		html:   cloudflareBlockPageFixture(),
		status: http.StatusForbidden,
		convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
			if strings.Contains(string(body), "Sorry, you have been blocked") {
				return strings.Repeat("laundered text ", 2000), nil
			}
			return "thin", nil
		},
	}
	h := browserHarvester(t, spy)
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if !result.Challenge {
		t.Fatalf("browser-detected challenge lost its flag: %#v", result)
	}
	if !strings.Contains(result.Error, "DID run") {
		t.Fatalf("browser-detected challenge did not report the rung ran: %q", result.Error)
	}
}

// TestBrowserEmptyRenderIsNotAnOutage pins review finding 8: an empty render
// is a COMPLETED attempt that found nothing, never a could-NOT-RUN outage.
// The blank page is what Chrome ACTUALLY serialises for an empty document —
// never the empty string, which the real worker cannot emit (review 2, B4).
func TestBrowserEmptyRenderIsNotAnOutage(t *testing.T) {
	spy := &browserSpyConverter{html: "<html><head></head><body></body></html>", status: 200}
	h := browserHarvester(t, spy)
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if strings.Contains(result.Error, "could NOT RUN") {
		t.Fatalf("a completed empty render was misreported as an outage: %q", result.Error)
	}
	if !strings.Contains(result.Error, "EMPTY") {
		t.Fatalf("empty render state not named: %q", result.Error)
	}
}

// TestBrowserPolicyDenialIsNotAnOutage pins review-2 B3: when the SSRF guard
// refuses the address, the terminal message says POLICY — never "tool
// outage", which would send the caller retrying a permanent refusal.
func TestBrowserPolicyDenialIsNotAnOutage(t *testing.T) {
	spy := &browserSpyConverter{
		err: fmt.Errorf("wrapped: %w", ErrBrowserPolicyDenied),
	}
	h := browserHarvester(t, spy)
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if strings.Contains(result.Error, "could NOT RUN") {
		t.Fatalf("a policy denial was misreported as an outage: %q", result.Error)
	}
	if !strings.Contains(result.Error, "SSRF guard refused") {
		t.Fatalf("policy denial state not named: %q", result.Error)
	}
	if !strings.Contains(result.Error, "not an outage") {
		t.Fatalf("policy denial did not disclaim the outage: %q", result.Error)
	}
}

// TestBrowserConverterOutageIsNamed pins review-2 B5: the browser BEAT the
// wall and rendered the article, then the conversion step failed. The user
// must hear "tool outage", never the definitive verdict that the wall won.
func TestBrowserConverterOutageIsNamed(t *testing.T) {
	rendered := "<html><body><h1>Recovered article</h1>" + strings.Repeat(
		"real rendered evidence ",
		100,
	) + "</body></html>"
	spy := &browserSpyConverter{
		html:   rendered,
		status: http.StatusOK,
		convertFn: func(_ context.Context, _, _ string, _ []byte) (string, error) {
			return "", errors.New("conversion worker not provisioned")
		},
	}
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if !strings.Contains(result.Error, "conversion step then failed") {
		t.Fatalf("converter outage collapsed into another arm: %q", result.Error)
	}
	if strings.Contains(result.Error, "still could not pass") {
		t.Fatalf("a converter outage was asserted as the wall winning: %q", result.Error)
	}
}

// TestBrowserThinRenderIsNeverStored pins review-2 S1: the HTML ladder's
// thin-page floor applies at the browser rung too. A JS paywall overlay that
// converts to a few hundred chars must NOT be cached as the article under
// method browser-chrome.
func TestBrowserThinRenderIsNeverStored(t *testing.T) {
	rendered := "<html><body><div id=paywall>Subscribe to keep reading this site</div></body></html>"
	spy := &browserSpyConverter{
		html:   rendered,
		status: http.StatusOK,
		convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
			if strings.Contains(string(body), "paywall") {
				return strings.Repeat("subscribe ", 30), nil // ~300 chars: above zero, below the 500 floor
			}
			return "wall", nil
		},
	}
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if result.Error == "" || result.Method == "browser-chrome" {
		t.Fatalf("a thin browser render was stored as content: %#v", result)
	}
}

// TestJinaTransportFailureKeepsTheEarlierStatus pins the pre-existing defect
// the wave review flagged out-of-scope (now ordered fixed): getBody returns
// status=0 on every transport-error path, so an unconditional
// lastStatus=status in the jina rung clobbers a genuine earlier 403 and the
// receipt reports HTTPStatus 0.
func TestJinaTransportFailureKeepsTheEarlierStatus(t *testing.T) {
	wall := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", "<html>checking your browser</html>"), nil
	})
	dead := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("fixture connection reset")
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: wall},
		Chrome:   &http.Client{Transport: wall},
		Jina:     &http.Client{Transport: dead},
		OA:       nonChallengeTransport(),
		Converter: legacyConverterFunc(
			func(context.Context, string, string, []byte) (string, error) { return "", nil },
		),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if result.HTTPStatus != http.StatusForbidden {
		t.Fatalf("HTTPStatus=%d, want the wall's 403 (jina's failed transport must not clobber it)", result.HTTPStatus)
	}
}

// recoveredArticleSpy converts a rendered "Recovered article" page to long
// real content and anything else (a wall) to thin text.
func recoveredArticleSpy(render func(headless bool) (string, int, error)) *browserSpyConverter {
	return &browserSpyConverter{
		render: render,
		convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
			if strings.Contains(string(body), "Recovered article") {
				return "# Recovered article\n\n" + strings.Repeat("real rendered evidence ", 100), nil
			}
			return "thin wall text", nil
		},
	}
}

const recoveredArticleHTML = "<html><body><h1>Recovered article</h1>real rendered evidence</body></html>"

// TestBrowserRungRendersHeadlessFirst pins headless-first: a render that
// passes on the first try never opens a visible window.
func TestBrowserRungRendersHeadlessFirst(t *testing.T) {
	spy := recoveredArticleSpy(func(bool) (string, int, error) { return recoveredArticleHTML, http.StatusOK, nil })
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if result.Error != "" || result.Method != "browser-chrome" {
		t.Fatalf("headless render not accepted: method=%q err=%q", result.Method, result.Error)
	}
	if fmt.Sprint(spy.modes) != "[true]" {
		t.Fatalf("browser modes=%v, want exactly one HEADLESS render [true]", spy.modes)
	}
}

// TestBrowserHeadedRetryOnlyAfterAWall: a headless render that meets a bot
// wall earns ONE headed retry, and the headed render's content wins.
func TestBrowserHeadedRetryOnlyAfterAWall(t *testing.T) {
	spy := recoveredArticleSpy(func(headless bool) (string, int, error) {
		if headless {
			return cloudflareBlockPageFixture(), http.StatusForbidden, nil
		}
		return recoveredArticleHTML, http.StatusOK, nil
	})
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if result.Error != "" || result.Method != "browser-chrome" {
		t.Fatalf("headed retry after a headless wall not accepted: method=%q err=%q", result.Method, result.Error)
	}
	if fmt.Sprint(spy.modes) != "[true false]" {
		t.Fatalf("browser modes=%v, want headless then headed [true false]", spy.modes)
	}
}

// TestBrowserHeadedRetryFailureKeepsTheWallVerdict: when the headed retry
// cannot launch (a display-less host), the completed headless attempt's
// verdict — it RAN and met a wall — stands; it never becomes an outage.
func TestBrowserHeadedRetryFailureKeepsTheWallVerdict(t *testing.T) {
	spy := recoveredArticleSpy(func(headless bool) (string, int, error) {
		if headless {
			return cloudflareBlockPageFixture(), http.StatusForbidden, nil
		}
		return "", 0, errors.New("headed Chrome needs a display")
	})
	h := browserHarvester(t, spy)
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if !result.Challenge || !strings.Contains(result.Error, "DID run against this wall") {
		t.Fatalf(
			"headed-launch failure erased the headless wall verdict: challenge=%v err=%q",
			result.Challenge,
			result.Error,
		)
	}
	if strings.Contains(result.Error, "could NOT RUN") {
		t.Fatalf("a completed headless attempt was misreported as an outage: %q", result.Error)
	}
	if fmt.Sprint(spy.modes) != "[true false]" {
		t.Fatalf("browser modes=%v, want [true false]", spy.modes)
	}
}
