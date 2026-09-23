package harvest

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// TestWithoutConsentMarkupDropsOnlyConsentContainers: every consent manager's
// container goes, the page's own content and a lookalike name stay.
func TestWithoutConsentMarkupDropsOnlyConsentContainers(t *testing.T) {
	page := `<html><body><article class="trusted-content">KEEP-ARTICLE</article>` +
		`<div id="onetrust-consent-sdk">VENDOR-ONETRUST</div><div class="qc-cmp2-container">VENDOR-TCF</div>` +
		`<div id="sp_message_container_1"><iframe></iframe>VENDOR-SP</div></body></html>`
	got := string(withoutConsentMarkup([]byte(page)))
	for _, gone := range []string{"VENDOR-ONETRUST", "VENDOR-TCF", "VENDOR-SP"} {
		if strings.Contains(got, gone) {
			t.Fatalf("consent container %s kept: %s", gone, got)
		}
	}
	if !strings.Contains(got, "KEEP-ARTICLE") {
		t.Fatalf("the page's own content was dropped: %s", got)
	}
	plain := []byte("<html><body>no banner here</body></html>")
	if !bytes.Equal(withoutConsentMarkup(plain), plain) {
		t.Fatal("a page with no consent markup was rewritten")
	}
}

// TestIsChallengeStillSeesAWallBesideAConsentBanner: stripping consent
// markup never hides a real wall on the same page.
func TestIsChallengeStillSeesAWallBesideAConsentBanner(t *testing.T) {
	page := `<html><body><div id="onetrust-consent-sdk">cookies</div>` +
		`<div id="cf-wrapper">Checking your browser before accessing</div></body></html>`
	if !isChallenge([]byte(page), http.StatusForbidden) {
		t.Fatal("a Cloudflare wall beside a consent banner was not judged a wall")
	}
}
