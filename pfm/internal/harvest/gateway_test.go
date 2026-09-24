package harvest

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
)

// browserConverter is a fakeConverter that also satisfies BrowserFetcher, so a
// test can drive the gateway's last two rungs. It records every call in order —
// the ORDER is the assertion in the headless-before-headed test, not a detail.
type browserConverter struct {
	fakeConverter
	mu    sync.Mutex
	calls []bool // one entry per FetchBrowser call: the headless flag it was given.
	reply func(headless bool) (string, int, error)
}

// FetchBrowser answers with reply, the render landing on the page requested.
func (b *browserConverter) FetchBrowser(_ context.Context, source string, headless bool) (string, int, string, error) {
	b.mu.Lock()
	b.calls = append(b.calls, headless)
	b.mu.Unlock()
	if b.reply == nil {
		return "", 0, "", errors.New("no browser reply configured")
	}
	html, status, err := b.reply(headless)
	return html, status, source, err
}

func (b *browserConverter) headlessFlags() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.calls...)
}

const gatewayWallBody = "<html><body>Checking your browser before accessing</body></html>"

func enabled() *bool { on := true; return &on }

// TestGatewayEscalatesProviderChallengeToBrowser is the whole point of the
// gateway. Before it existed, providerGet ran ONE plain client: a provider
// record page behind a JS wall was terminal, while the identical wall on the
// generic web ladder was passed by the browser rung. Same network, same wall,
// two outcomes depending on which door the call entered.
func TestGatewayEscalatesProviderChallengeToBrowser(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	walled := func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", gatewayWallBody), nil
	}
	browser := &browserConverter{reply: func(bool) (string, int, error) {
		return "<html><body>the real record page</body></html>", http.StatusOK, nil
	}}
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(walled)},
		Chrome:      &http.Client{Transport: roundTripFunc(walled)},
		Converter:   browser,
		BrowserRung: enabled(),
	})
	got, err := h.providerGet(context.Background(), "https://ipfs-catalog.test/md5/abc", nil)
	if err != nil {
		t.Fatalf("providerGet through the gateway error = %v, want the browser rung to pass the wall", err)
	}
	if !strings.Contains(string(got.body), "the real record page") {
		t.Fatalf("providerGet body = %q, want the browser rung's render", got.body)
	}
	if flags := browser.headlessFlags(); len(flags) != 1 || !flags[0] {
		t.Fatalf("browser calls = %v, want exactly one HEADLESS call", flags)
	}
}

// TestGatewayBrowserTriesHeadlessBeforeHeaded pins the ordering the operator
// asked for: headless ALWAYS first, a visible window only as the last resort.
// A headed launch takes over the operator's desktop, so an implementation that
// reversed these — or spent the headed rung without trying headless — would be
// a real harm this test exists to catch.
func TestGatewayBrowserTriesHeadlessBeforeHeaded(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	walled := func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", gatewayWallBody), nil
	}
	browser := &browserConverter{reply: func(headless bool) (string, int, error) {
		if headless {
			return gatewayWallBody, http.StatusForbidden, nil // headless meets the wall
		}
		return "<html><body>headed render won</body></html>", http.StatusOK, nil
	}}
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(walled)},
		Chrome:      &http.Client{Transport: roundTripFunc(walled)},
		Converter:   browser,
		BrowserRung: enabled(),
	})
	got, err := h.providerGet(context.Background(), "https://doi-viewer.test/record/1", nil)
	if err != nil {
		t.Fatalf("providerGet error = %v", err)
	}
	if !strings.Contains(string(got.body), "headed render won") {
		t.Fatalf("body = %q, want the headed render", got.body)
	}
	flags := browser.headlessFlags()
	if len(flags) != 2 || !flags[0] || flags[1] {
		t.Fatalf("browser calls = %v, want exactly [headless, headed] in that order", flags)
	}
}

// TestGatewayLaterRungErrorKeepsTheChallengeAnswer guards the precedence rule.
// Rung 1 ANSWERED — the source served a challenge page. Rung 2 never reached
// the server. Reporting rung 2's transport error would tell the operator their
// DNS is broken when the truth is that the source is behind a wall, sending
// them to fix the wrong thing. A rung that answered always outranks a rung that
// failed to connect.
func TestGatewayLaterRungErrorKeepsTheChallengeAnswer(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	direct := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", gatewayWallBody), nil
	})}
	chrome := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: no such host")
	})}
	h := mustNew(t, Options{CacheDir: t.TempDir(), Client: direct, Chrome: chrome, Converter: &fakeConverter{}})
	got, err := h.providerGet(context.Background(), "https://ipfs-catalog.test/md5/abc", nil)
	if err != nil {
		t.Fatalf("providerGet error = %v, want the challenge ANSWER to survive the later rung's transport failure", err)
	}
	if got.status != http.StatusForbidden || !strings.Contains(string(got.body), "Checking your browser") {
		t.Fatalf("providerGet = status %d body %q, want the rung-1 challenge page", got.status, got.body)
	}
}

// TestGatewayNeverSpendsBrowserRungOnBinaryDownload: the browser rung returns
// rendered HTML. Escalating a PDF download to it cannot produce the PDF's
// bytes, so doing it would burn a browser launch — and, at the headed rung, a
// window on the operator's desktop — to arrive at a guaranteed failure.
func TestGatewayNeverSpendsBrowserRungOnBinaryDownload(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	walled := func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", gatewayWallBody), nil
	}
	browser := &browserConverter{reply: func(bool) (string, int, error) {
		return "<html><body>should never be asked for</body></html>", http.StatusOK, nil
	}}
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(walled)},
		Chrome:      &http.Client{Transport: roundTripFunc(walled)},
		Converter:   browser,
		BrowserRung: enabled(),
	})
	if _, err := h.providerDownload(context.Background(), "https://md5-catalog.test/file.pdf", nil, 1<<20); err != nil {
		t.Fatalf("providerDownload error = %v, want the walled response returned without a browser launch", err)
	}
	if flags := browser.headlessFlags(); len(flags) != 0 {
		t.Fatalf("browser calls = %v, want NONE for a binary download", flags)
	}
}

// TestGatewayClientJarSemantics pins both branches of the jar rule. A supplied
// jar always wins; a nil jar LEAVES THE BASE CLIENT'S OWN jar in place, which
// is what the generic web ladder needs — it passes no jar and must not have a
// session an earlier rung established silently cleared. The reviewer flagged
// this branch as untested and therefore free to drift.
func TestGatewayClientJarSemantics(t *testing.T) {
	baseJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	requestJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	base := &http.Client{Jar: baseJar}

	if got := gatewayClient(base, requestJar); got.Jar != requestJar {
		t.Fatalf("gatewayClient with a jar used %v, want the supplied jar", got.Jar)
	}
	if got := gatewayClient(base, nil); got.Jar != baseJar {
		t.Fatalf("gatewayClient with a nil jar used %v, want the base client's own jar preserved", got.Jar)
	}
	if got := gatewayClient(nil, nil); got.Jar != nil {
		t.Fatalf("gatewayClient with no base and no jar used %v, want no jar", got.Jar)
	}
	// The clone must never mutate the shared base client.
	if base.Jar != baseJar {
		t.Fatalf("gatewayClient mutated the shared base client's jar")
	}
}

// TestGatewayFetchHonorsTrustedOrigin: retrieveGateway used to call
// validateFetchURL(req.url, false) unconditionally, refusing the operator's
// own loopback origin as a private host even though gatewayAttempt (which
// every rung underneath actually calls) already honors trustedOrigin. The two
// entry points must agree.
func TestGatewayFetchHonorsTrustedOrigin(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "application/json", `{"ok":true}`), nil
	})}
	h := mustNew(t, Options{CacheDir: t.TempDir(), Client: client, Chrome: client, Converter: &fakeConverter{}})
	got, err := h.retrieveGateway(context.Background(), gatewayRequest{
		url:           "http://127.0.0.1:9/healthz",
		client:        client,
		trustedOrigin: true,
		policy:        gatewayNoEscalate,
	})
	if err != nil {
		t.Fatalf("retrieveGateway(trustedOrigin loopback) error = %v, want it accepted like gatewayAttempt does", err)
	}
	if got.status != http.StatusOK {
		t.Fatalf("retrieveGateway(trustedOrigin loopback) status = %d, want 200", got.status)
	}
}

// TestGatewayAttemptTrustedOriginNilClientErrors: gatewayRequestClient
// dereferences *req.client unconditionally for a trusted origin. A nil
// client is a caller bug, not a network failure, and must surface as an
// error naming the missing client rather than panicking the process.
func TestGatewayAttemptTrustedOriginNilClientErrors(t *testing.T) {
	_, err := gatewayAttempt(context.Background(), gatewayRequest{
		url:           "http://127.0.0.1:9/healthz",
		trustedOrigin: true,
	})
	if err == nil {
		t.Fatal("gatewayAttempt(trustedOrigin, nil client) error = nil, want an error naming the missing client")
	}
	if !strings.Contains(err.Error(), "client") {
		t.Fatalf("gatewayAttempt(trustedOrigin, nil client) error = %q, want it to name the missing client", err)
	}
}

// TestGatewayBothRungsFailingReportsBothErrors: when neither rung reached the
// server, reporting only the first hides the rung carrying the more diagnostic
// failure.
func TestGatewayBothRungsFailingReportsBothErrors(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	direct := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("direct rung exploded")
	})}
	chrome := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("chrome rung exploded")
	})}
	h := mustNew(t, Options{CacheDir: t.TempDir(), Client: direct, Chrome: chrome, Converter: &fakeConverter{}})
	_, err := h.providerGet(context.Background(), "https://ipfs-catalog.test/md5/abc", nil)
	if err == nil {
		t.Fatal("providerGet error = nil, want both rungs' failures reported")
	}
	for _, want := range []string{"direct rung exploded", "chrome rung exploded"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to name %q", err, want)
		}
	}
}

// TestProviderSearchFailsAtTheChallengeBody: a search_literature search page behind a
// wall is reported the moment the walled body arrives. The browser rungs are
// never spent on it — they held the call about 60 s and still met the wall.
func TestProviderSearchFailsAtTheChallengeBody(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	walled := func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", gatewayWallBody), nil
	}
	browser := &browserConverter{reply: func(bool) (string, int, error) {
		return "<html><body>a browser render</body></html>", http.StatusOK, nil
	}}
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(walled)},
		Chrome:      &http.Client{Transport: roundTripFunc(walled)},
		Converter:   browser,
		BrowserRung: enabled(),
	})
	got, err := h.providerSearch(context.Background(), "https://ipfs-catalog.test/search?q=deep")
	if err != nil {
		t.Fatalf("providerSearch error = %v, want the walled answer", err)
	}
	if got.status != http.StatusForbidden || !providerChallenge(got.body, got.status) {
		t.Fatalf("providerSearch = %d %q, want the walled 403 body", got.status, got.body)
	}
	if flags := browser.headlessFlags(); len(flags) != 0 {
		t.Fatalf("browser calls = %v, want none for a discovery search", flags)
	}
}
