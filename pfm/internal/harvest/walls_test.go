package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// nytConsentDialog is a Fides privacy-preference dialog as a news site lays
// it over the article: thousands of words of purposes and vendors.
const nytConsentDialog = `<div id="fides-overlay"><div id="fides-modal" class="fides-modal-container">` +
	`<h2>Manage Privacy Preferences</h2><p>We and our vendors use cookies and similar methods to recognize visitors.</p>` +
	`<h3>Purposes</h3><p>Store and/or access information on a device. IAB TCF.</p><h3>Vendors176 vendor(s)</h3>` +
	`<ul><li>6sense</li><li>Adform</li></ul></div></div>`

// articlePreview is the paragraphs a paywalled article serves signed-out.
var articlePreview = strings.Repeat(
	`<p>The newspaper sued two technology companies for copyright infringement on Wednesday, opening a new front.</p>`,
	8,
)

// signedOutPosts is the posts a logged-out app profile still shows.
var signedOutPosts = strings.Repeat(
	`<article data-testid="tweet"><p>Join us for a new aerospace festival with air shows and space exhibits.</p></article>`,
	8,
)

// xBottomBar is the logged-out app's sign-in gate.
const xBottomBar = `<div data-testid="BottomBar"><p>Don't miss what's happening. People on X are the first to know.</p>` +
	`<a href="/login" data-testid="loginButton">Log in</a><a href="/i/flow/signup" data-testid="signupButton">Sign up</a></div>`

// TestWallsAreNamedNeverStoredSilently: a consent dialog is never stored as
// the page; a paywalled article keeps its preview and names the paywall from
// its markup; a logged-out app page names its login wall, and a page holding
// nothing but the gate fails naming it; prose asking for a subscription alone
// names nothing.
func TestWallsAreNamedNeverStoredSilently(t *testing.T) {
	const paywall = "paywalled: only the preview the site serves without a subscription was read"
	const loginWall = "login wall: only what the site shows signed-out was read"
	newsURL := "https://news.example.com/2023/12/27/business/lawsuit.html"
	for _, tc := range []struct {
		name, source, page, partial, absent, kept, failure string
	}{
		{
			name:   "a paywalled article under a consent dialog",
			source: newsURL,
			page: `<html><head><script type="application/ld+json">{"@type":"NewsArticle","isAccessibleForFree":false,` +
				`"hasPart":{"@type":"WebPageElement","isAccessibleForFree":false,"cssSelector":".meteredContent"}}</script>` +
				`</head><body>` + nytConsentDialog + `<article><h1>The Times Sues</h1><section class="meteredContent">` +
				articlePreview + `</section></article><div id="gateway-content" data-testid="gateway">` +
				`<p>Subscribe to continue reading. Enjoy unlimited access to all of The Times.</p></div></body></html>`,
			partial: paywall,
			absent:  "Manage Privacy Preferences",
			kept:    "opening a new front",
		},
		{
			name:   "a gateway block inside a paywall element, no schema.org flag",
			source: newsURL,
			page: `<html><body><article>` + articlePreview + `</article><div class="paywall-gateway">` +
				`<p>Subscribe to continue reading.</p></div></body></html>`,
			partial: paywall,
			kept:    "opening a new front",
		},
		{
			name:   "a free article asking for a newsletter in its prose",
			source: newsURL,
			page: `<html><head><script type="application/ld+json">{"@type":"NewsArticle","isAccessibleForFree":true}</script>` +
				`</head><body><article>` + articlePreview + `<p>If you liked this, please subscribe to our newsletter ` +
				`and sign in to comment.</p></article></body></html>`,
			kept: "please subscribe to our newsletter",
		},
		{
			name:    "a logged-out app profile",
			source:  "https://x.com/NASA",
			page:    `<html><body><main>` + signedOutPosts + `</main>` + xBottomBar + `</body></html>`,
			partial: loginWall,
			kept:    "aerospace festival",
		},
		{
			name:   "a logged-out app profile rendered for a crawler, its gate an onboarding link",
			source: "https://x.com/NASA",
			page: `<html><body><main>` + signedOutPosts + `</main><div class="log-in">` +
				`<a href="/i/jf/onboarding/web?mode=login&amp;redirect_after_login=%2FNASA">Log in</a></div></body></html>`,
			partial: loginWall,
			kept:    "aerospace festival",
		},
		{
			name:    "a logged-out app page holding nothing but the gate",
			source:  "https://x.com/NASA",
			page:    `<html><body><main><h1>Sign in to X</h1></main>` + xBottomBar + `</body></html>`,
			failure: "login wall",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			served := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == tc.source {
					return response(request, http.StatusOK, "text/html; charset=UTF-8", tc.page), nil
				}
				return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
			})
			converter := &browserSpyConverter{
				convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
					text := scriptRe.ReplaceAllString(string(body), " ")
					text = htmlTagRe.ReplaceAllString(styleRe.ReplaceAllString(text, " "), " ")
					return strings.Join(strings.Fields(text), " "), nil
				},
			}
			h := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: served},
				Chrome:      &http.Client{Transport: served},
				Jina:        &http.Client{Transport: served},
				OA:          &http.Client{Transport: served},
				Converter:   converter,
				BrowserRung: browserOff(),
				Clock:       newPacingClock(),
			})
			result := h.FetchWithOptions(context.Background(), tc.source, FetchOptions{Refresh: true})
			if tc.failure != "" {
				if result.Error == "" || !strings.Contains(result.Error, tc.failure) {
					t.Fatalf("a gate-only page was not a failure naming %q (error %q):\n%.600s",
						tc.failure, result.Error, result.Content)
				}
				return
			}
			if result.Error != "" {
				t.Fatalf("the page failed: %s", result.Error)
			}
			if tc.partial != "" && !strings.Contains(result.Partial, tc.partial) {
				t.Fatalf("partial = %q, want %q", result.Partial, tc.partial)
			}
			if tc.partial == "" && result.Partial != "" {
				t.Fatalf("a page with no wall markup named %q", result.Partial)
			}
			if tc.absent != "" && strings.Contains(result.Content, tc.absent) {
				t.Fatalf("the consent dialog was stored as content:\n%.600s", result.Content)
			}
			if !strings.Contains(result.Content, tc.kept) {
				t.Fatalf("the page's own content %q was dropped:\n%.600s", tc.kept, result.Content)
			}
		})
	}
}
