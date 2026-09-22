package harvest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLegacyUnresolvedBotChallengeIsNeverCached(t *testing.T) {
	challenge := "<html><body>Are you a robot? verify you are human</body></html>"
	wallTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", challenge), nil
	})
	missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		ContactEmail: "test@example.org",
		CacheDir:     t.TempDir(),
		Client:       &http.Client{Transport: wallTransport},
		Chrome:       &http.Client{Transport: wallTransport},
		Jina:         &http.Client{Transport: missingTransport},
		OA:           &http.Client{Transport: missingTransport},
		Converter: legacyConverterFunc(
			func(_ context.Context, _, _ string, raw []byte) (string, error) { return string(raw), nil },
		),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if result.Error == "" || !strings.Contains(strings.ToLower(result.Error), "challenge") ||
		!strings.Contains(result.Error, "Rungs tried: direct, chrome-impersonation, jina, defuddle, wayback") {
		t.Fatalf("unresolved challenge receipt=%#v", result)
	}
	var artifacts []string
	_ = filepath.Walk(h.options.CacheDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && filepath.Base(path) != "stats.jsonl" {
			// stats.jsonl is the fetch-outcome SCOREBOARD (telemetry), not a
			// content artifact — a failing fetch may still record its outcome.
			artifacts = append(artifacts, path)
		}
		return nil
	})
	if len(artifacts) != 0 {
		t.Fatalf("unresolved challenge entered cache: %#v", artifacts)
	}
}

// TestCloudflare403BlockPageEscalatesInsteadOfCachingSuccess pins the rung-1
// escalation gate against a REAL Cloudflare "Sorry, you have been blocked"
// (error 1020) interstitial, not the short synthetic wall text the sibling
// tests above use. Genuine Cloudflare block pages ship several KB of inline
// CSS, so the raw body comfortably exceeds isChallenge's 4000-byte
// weak-marker gate ("cloudflare"/"attention required" only count under that
// gate) — and its copy ("Sorry, you have been blocked", "Why have I been
// blocked?") matches none of the length-independent strong phrases either.
// A 403 body this large, once converted, also clears the html "thin content"
// floor (500 chars), so nothing forces a retry: the wall is cached as an
// ordinary successful "html" result at the direct rung. Per
// the retired Python dispatch.py's html rescue ladder (`git show
// 4edf9c5^:harvester/src/harvester/dispatch.py`), a Cloudflare 403 must escalate
// through chrome-impersonation, then jina, then defuddle, then wayback, and
// only report a
// named challenge failure — never a cached success — once every rung is
// exhausted.
func TestCloudflare403BlockPageEscalatesInsteadOfCachingSuccess(t *testing.T) {
	blockPage := cloudflareBlockPageFixture()
	if len(blockPage) <= 4000 {
		t.Fatalf(
			"fixture must exceed the 4000-byte weak-marker gate to reproduce the live page, got %d bytes",
			len(blockPage),
		)
	}
	wallTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", blockPage), nil
	})
	missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: wallTransport},
		Chrome:   &http.Client{Transport: wallTransport},
		Jina:     &http.Client{Transport: missingTransport},
		OA:       &http.Client{Transport: missingTransport},
		Converter: legacyConverterFunc(
			func(_ context.Context, _, _ string, raw []byte) (string, error) { return string(raw), nil },
		),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), "https://blocked.example.test/article")
	if result.Error == "" {
		t.Fatalf("Cloudflare 403 block page returned as a cached SUCCESS instead of escalating: %#v", result)
	}
	if !result.Challenge || !strings.Contains(strings.ToLower(result.Error), "challenge") {
		t.Fatalf("Cloudflare 403 block page not classified as a challenge failure: %#v", result)
	}
	if !strings.Contains(result.Error, "Rungs tried: direct, chrome-impersonation, jina, defuddle, wayback") {
		t.Fatalf("Cloudflare 403 block page did not escalate the full rung ladder: %#v", result)
	}
	var artifacts []string
	_ = filepath.Walk(h.options.CacheDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && filepath.Base(path) != "stats.jsonl" {
			// stats.jsonl is the fetch-outcome SCOREBOARD (telemetry), not a
			// content artifact — a failing fetch may still record its outcome.
			artifacts = append(artifacts, path)
		}
		return nil
	})
	if len(artifacts) != 0 {
		t.Fatalf("blocked page entered the positive cache: %#v", artifacts)
	}
}

// cloudflareBlockPageFixture reproduces the shape of Cloudflare's live
// "Attention Required! | Cloudflare" / error-1020 interstitial: real inline
// CSS bulk, an h1 reading "Sorry, you have been blocked", and a footer naming
// Cloudflare and a Ray ID — the exact wording an operator would paste from a
// live block, not a stripped-down synthetic "captcha" string.
func cloudflareBlockPageFixture() string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"en-US\"><head><title>Attention Required! | Cloudflare</title>")
	b.WriteString("<meta charset=\"UTF-8\" /><meta name=\"robots\" content=\"noindex, nofollow\" />")
	b.WriteString("<style type=\"text/css\">")
	b.WriteString(
		strings.Repeat(
			".cf-error-details{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;margin:0;padding:0;color:#494949} ",
			60,
		),
	)
	b.WriteString("</style></head><body><div class=\"cf-wrapper\"><div class=\"cf-error-details\">")
	b.WriteString("<h1>Sorry, you have been blocked</h1>")
	b.WriteString("<p>You are unable to access blocked.example.test</p>")
	b.WriteString(
		"<p>This website is using a security service to protect itself from online attacks. The action you just performed triggered the security solution. There are several actions that could trigger this block including submitting a certain word or phrase, a SQL command or malformed data.</p>",
	)
	b.WriteString("<h2>Why have I been blocked?</h2>")
	b.WriteString(
		"<p>What can I do to resolve this? You can email the site owner to let them know you were blocked. Please include what you were doing when this page came up and the Cloudflare Ray ID found at the bottom of this page.</p>",
	)
	b.WriteString(
		"<div class=\"cf-error-footer\">Cloudflare Ray ID: <strong>00000000000000</strong> &bull; Your IP: <strong>203.0.113.1</strong> &bull; Performance &amp; security by <a href=\"https://www.cloudflare.com\">Cloudflare</a></div>",
	)
	b.WriteString("</div></div></body></html>")
	return b.String()
}

func TestLegacyChallengeCanRecoverAtChromeAndPersistsTrace(t *testing.T) {
	direct := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", "<html>checking your browser</html>"), nil
	})
	rich := "<html><body><h1>Recovered article</h1>" + strings.Repeat("scientific evidence ", 100) + "</body></html>"
	chrome := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/html", rich), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: direct},
		Chrome:   &http.Client{Transport: chrome},
		Converter: legacyConverterFunc(
			func(_ context.Context, _, _ string, raw []byte) (string, error) { return string(raw), nil },
		),
	})
	result := h.Fetch(context.Background(), "https://blocked.example.test/recovered")
	if result.Error != "" || result.Method != "chrome-impersonation" ||
		strings.Join(result.Rungs, ",") != "direct,chrome-impersonation" {
		t.Fatalf("Chrome recovery=%#v", result)
	}
	raw, err := os.ReadFile(result.Path)
	if err != nil || !strings.Contains(string(raw), "rungs: direct, chrome-impersonation") {
		t.Fatalf("Chrome trace artifact=%q err=%v", string(raw), err)
	}
}

func TestLegacyCitationPDFMetaRescueCachesThePublisherSource(t *testing.T) {
	const publisher = "https://publisher.example.test/walled-article"
	const mirror = "https://repository.example.test/open-paper.pdf"
	wall := `<html><head><meta name="citation_pdf_url" content="` + mirror + `"></head><body>checking your browser</body></html>`
	var publisherCalls atomic.Int64
	var mirrorCalls atomic.Int64
	documentTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case publisher:
			publisherCalls.Add(1)
			return response(request, http.StatusForbidden, "text/html", wall), nil
		case mirror:
			mirrorCalls.Add(1)
			return response(request, http.StatusOK, "application/pdf", "%PDF-1.7 open fixture"), nil
		default:
			return response(request, http.StatusNotFound, "application/json", `{}`), nil
		}
	})
	missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: documentTransport},
		Chrome:   &http.Client{Transport: documentTransport},
		Jina:     &http.Client{Transport: missingTransport},
		OA:       &http.Client{Transport: missingTransport},
		Converter: legacyConverterFunc(func(_ context.Context, kind, _ string, _ []byte) (string, error) {
			if kind == "pdf" {
				return "# Open paper\n\nRecovered through citation metadata.", nil
			}
			return "", nil
		}),
		BrowserRung: browserOff(),
	})
	first := h.Fetch(context.Background(), publisher)
	wantRungs := "direct,chrome-impersonation,jina,defuddle,oa:citation_pdf_url"
	if first.Error != "" || first.Source != publisher || strings.Join(first.Rungs, ",") != wantRungs {
		t.Fatalf("citation PDF rescue=%#v want rungs=%s", first, wantRungs)
	}
	firstPublisherCalls, firstMirrorCalls := publisherCalls.Load(), mirrorCalls.Load()
	second := h.Fetch(context.Background(), publisher)
	if second.Error != "" || second.CacheStatus != "hit" || publisherCalls.Load() != firstPublisherCalls ||
		mirrorCalls.Load() != firstMirrorCalls {
		t.Fatalf(
			"citation PDF publisher cache=%#v publisher=%d->%d mirror=%d->%d",
			second,
			firstPublisherCalls,
			publisherCalls.Load(),
			firstMirrorCalls,
			mirrorCalls.Load(),
		)
	}
}

func TestLegacySelfReferentialOACandidateCannotRecurse(t *testing.T) {
	const source = "https://publisher.example.test/10.1234/recursive"
	const candidate = "https://mirror.example.test/10.1234/recursive.pdf"
	var providerCalls atomic.Int64
	oaTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Host, "unpaywall") {
			providerCalls.Add(1)
			return jsonResponse(
				request,
				`{"is_oa":true,"oa_status":"green","best_oa_location":{"url_for_pdf":"`+candidate+`","version":"publishedVersion"}}`,
			), nil
		}
		return jsonResponse(request, `{}`), nil
	})
	wallTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", "<html>checking your browser</html>"), nil
	})
	missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		ContactEmail: "test@example.org", // unpaywall is gated on an operator email
		CacheDir:     t.TempDir(),
		Client:       &http.Client{Transport: wallTransport},
		Chrome:       &http.Client{Transport: wallTransport},
		Jina:         &http.Client{Transport: missingTransport},
		OA:           &http.Client{Transport: oaTransport},
		Converter: legacyConverterFunc(
			func(_ context.Context, _, _ string, raw []byte) (string, error) { return string(raw), nil },
		),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), source)
	if result.Error == "" || providerCalls.Load() != 1 {
		t.Fatalf("recursive OA result=%#v unpaywall calls=%d", result, providerCalls.Load())
	}
	if !strings.Contains(strings.Join(result.Rungs, ","), "oa:unpaywall") ||
		result.Rungs[len(result.Rungs)-1] != "wayback" {
		t.Fatalf("recursive OA trace=%#v", result.Rungs)
	}
}

// forumWallFixture reproduces a forum anti-bot interstitial served as HTTP 200:
// the wall copy sits beside several KB of ordinary page chrome, so neither
// the status code nor the 4000-byte weak-marker gate flags it — only the wall
// phrase itself can.
func forumWallFixture(phrase string) string {
	return "<html><head><title>Forum</title></head><body><h1>" + phrase + "</h1>" +
		"<p>" + strings.Repeat("Community discussion threads, member profiles and weekly digests. ", 80) + "</p>" +
		"<div class=\"g-recaptcha\" data-sitekey=\"fixture\"></div></body></html>"
}

// forumArticleFixture reproduces a long real page (a Reddit thread, a news
// article) that merely quotes or discusses a bot-wall phrase in passing —
// HTTP 200, well past the 4000-char short-body gate, no captcha widget. A
// page like this is content, never a wall, even though it contains the exact
// phrase text.
func forumArticleFixture(phrase string) string {
	return "<html><head><title>Forum thread</title></head><body><h1>Outage megathread</h1>" +
		"<p>Yesterday several members reported seeing the message \"" + phrase + "\" when the site's edge provider had a bad rollout. " +
		strings.Repeat("Replies debated the outage at length, sharing screenshots, timestamps and workarounds. ", 80) +
		"</p></body></html>"
}

// forumArticleCaptchaWordFixture reproduces a long real page that discusses
// the wall AND uses the plain prose word "captcha" ("they made me solve a
// captcha to prove your humanity...") — no actual widget markup, so it must
// stay content: the corroboration check requires widget MARKUP, never the
// bare word, or this exact case (a Reddit thread describing the wall) would
// still be misflagged.
func forumArticleCaptchaWordFixture(phrase string) string {
	return "<html><head><title>Forum thread</title></head><body><h1>Outage megathread</h1>" +
		"<p>One reply read: \"they made me solve a captcha to " + strings.ToLower(phrase) + " before it let me post.\" " +
		strings.Repeat("Other replies debated the outage at length, sharing screenshots and workarounds. ", 80) +
		"</p></body></html>"
}

// TestForumWallPhrasesNeedCorroboration pins isChallenge's rule for the forum
// wall phrases ("prove your humanity", "blocked by network security",
// "blocked due to a network policy"): unlike the length-independent strong
// phrases, these three are ALSO the exact words a long real page uses when it
// merely quotes or discusses the wall (a Reddit thread, a news article) — so
// they only count as a challenge when something else corroborates it: a
// 403/429/503 status, a short body, or an actual captcha widget in the
// markup. A long HTTP-200 page that merely quotes a phrase is content.
func TestForumWallPhrasesNeedCorroboration(t *testing.T) {
	type row struct {
		name          string
		body          string
		status        int
		wantChallenge bool
	}
	var rows []row
	for _, phrase := range []string{
		"Prove your humanity",
		"You've been blocked by network security.",
		"Your request has been blocked due to a network policy.",
	} {
		article := forumArticleFixture(phrase)
		if contentChars(article) <= 4000 {
			t.Fatalf("%q: article fixture must exceed the 4000-char gate, got %d", phrase, contentChars(article))
		}
		rows = append(rows, row{
			name: phrase + "/long 200 article quoting it", body: article, status: http.StatusOK, wantChallenge: false,
		})
	}
	// One representative phrase covers the corroborating-signal rows; the
	// per-phrase loop above is what pins the false-positive fix itself.
	const phrase = "Prove your humanity"
	shortWall := "<html><head><title>Forum</title></head><body><h1>" + phrase + "</h1></body></html>"
	if contentChars(shortWall) >= 4000 {
		t.Fatalf("short wall fixture must be under the 4000-char gate, got %d", contentChars(shortWall))
	}
	widgetWall := forumWallFixture(phrase) // carries a g-recaptcha div
	if contentChars(widgetWall) <= 4000 {
		t.Fatalf("widget wall fixture must exceed the 4000-char gate, got %d", contentChars(widgetWall))
	}
	longArticle := forumArticleFixture(phrase)
	captchaWordArticle := forumArticleCaptchaWordFixture(phrase)
	if contentChars(captchaWordArticle) <= 4000 {
		t.Fatalf(
			"captcha-word article fixture must exceed the 4000-char gate, got %d",
			contentChars(captchaWordArticle),
		)
	}
	rows = append(rows,
		row{name: "short 200 wall page", body: shortWall, status: http.StatusOK, wantChallenge: true},
		row{name: "long 403", body: longArticle, status: http.StatusForbidden, wantChallenge: true},
		row{name: "long 429", body: longArticle, status: http.StatusTooManyRequests, wantChallenge: true},
		row{name: "long 200 with a reCAPTCHA widget", body: widgetWall, status: http.StatusOK, wantChallenge: true},
		row{
			name: "long 200 thread quoting the phrase and the plain word captcha", body: captchaWordArticle,
			status: http.StatusOK, wantChallenge: false,
		},
	)

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			wallTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, tc.status, "text/html", tc.body), nil
			})
			missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusNotFound, "application/json", `{}`), nil
			})
			h := mustNew(t, Options{
				CacheDir: t.TempDir(),
				Client:   &http.Client{Transport: wallTransport},
				Chrome:   &http.Client{Transport: wallTransport},
				Jina:     &http.Client{Transport: wallTransport},
				OA:       &http.Client{Transport: missingTransport},
				Converter: legacyConverterFunc(
					func(_ context.Context, _, _ string, raw []byte) (string, error) { return string(raw), nil },
				),
				BrowserRung: browserOff(),
			})
			result := h.Fetch(context.Background(), "https://forum.example.test/r/thread/1")
			var artifacts []string
			_ = filepath.Walk(h.options.CacheDir, func(path string, info os.FileInfo, err error) error {
				if err == nil && info != nil && !info.IsDir() && filepath.Base(path) != "stats.jsonl" {
					artifacts = append(artifacts, path)
				}
				return nil
			})
			if tc.wantChallenge {
				if result.Error == "" {
					t.Fatalf(
						"wanted a challenge failure, got a SUCCESS: method=%q rungs=%v",
						result.Method,
						result.Rungs,
					)
				}
				if !result.Challenge || !strings.Contains(strings.ToLower(result.Error), "challenge") {
					t.Fatalf("wanted a challenge failure: %#v", result)
				}
				if len(artifacts) != 0 {
					t.Fatalf("challenge entered the positive cache: %#v", artifacts)
				}
			} else {
				if result.Error != "" || result.Challenge {
					t.Fatalf("wanted plain content, got a challenge failure: %#v", result)
				}
				if len(artifacts) == 0 {
					t.Fatalf("real content never entered the cache: %#v", result)
				}
			}
		})
	}
}

// TestDirectRungSendsProvenanceReferer pins the cheapest way past a wall that
// opens for any Referer: the direct rung itself carries one, so the real page
// arrives on the first request instead of the wall being escalated (or cached).
func TestDirectRungSendsProvenanceReferer(t *testing.T) {
	page := "<html><body><h1>Thread title</h1>" + strings.Repeat("original post and replies ", 60) + "</body></html>"
	var directReferer atomic.Value
	directReferer.Store("")
	direct := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		referer := request.Header.Get("Referer")
		directReferer.Store(referer)
		if referer == "" {
			return response(request, http.StatusOK, "text/html", forumWallFixture("Prove your humanity")), nil
		}
		return response(request, http.StatusOK, "text/html", page), nil
	})
	missingTransport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: direct},
		Chrome:   &http.Client{Transport: missingTransport},
		Jina:     &http.Client{Transport: missingTransport},
		OA:       &http.Client{Transport: missingTransport},
		Converter: legacyConverterFunc(
			func(_ context.Context, _, _ string, raw []byte) (string, error) { return string(raw), nil },
		),
		BrowserRung: browserOff(),
	})
	result := h.Fetch(context.Background(), "https://forum.example.test/r/thread/2")
	if got := directReferer.Load().(string); got == "" {
		t.Fatalf(
			"direct rung sent no Referer; method=%q rungs=%v error=%q",
			result.Method,
			result.Rungs,
			result.Error,
		)
	}
	if result.Error != "" || result.Method != rungDirect || strings.Join(result.Rungs, ",") != rungDirect {
		t.Fatalf(
			"Referer-gated page not fetched at the direct rung: method=%q rungs=%v error=%q",
			result.Method,
			result.Rungs,
			result.Error,
		)
	}
	if !strings.Contains(result.Content, "original post and replies") ||
		strings.Contains(result.Content, "Prove your humanity") {
		t.Fatalf("direct rung content is not the real page: %q", result.Content)
	}
}
