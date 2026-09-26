package harvest

import (
	"net/http"
	"strings"
	"testing"
)

// walledResult is the result the web ladder leaves for a challenge page every
// rung met: the harvester's own FailureMessage (net.go) as its error, the
// rungs that ran, no word the site sent.
func walledResult(rungs []string) Result {
	const source = "https://news.example.com/2023/12/27/business/story.html"
	return Result{
		Source:     source,
		ErrorKind:  errorKindChallenge,
		Challenge:  true,
		HTTPStatus: http.StatusForbidden,
		Rungs:      rungs,
		Error:      FailureMessage(source, http.StatusForbidden, "", true, false),
	}
}

// TestChallengeMetByTheBrowserSaysARetryMayPass: a wall the real-browser rung
// met is intermittent (the same page passed a browser read seconds later), so
// the failure says a retry may pass and still names another copy; a ladder
// whose browser never ran keeps the retry-is-futile wording.
func TestChallengeMetByTheBrowserSaysARetryMayPass(t *testing.T) {
	withBrowser := PublicFailureMessage(walledResult(
		[]string{"direct", "chrome-impersonation", "jina", "defuddle", "browser", "wayback"}))
	for _, want := range []string{"a retry later may pass", "another copy", "Rungs tried: direct, chrome-impersonation, jina, defuddle, browser, wayback"} {
		if !strings.Contains(withBrowser, want) {
			t.Errorf("the browser-met wall lacks %q:\n%s", want, withBrowser)
		}
	}
	if strings.Contains(withBrowser, "will meet the same wall") {
		t.Errorf("the browser-met wall calls a retry futile:\n%s", withBrowser)
	}
	withoutBrowser := PublicFailureMessage(walledResult([]string{"direct", "chrome-impersonation", "wayback"}))
	if !strings.Contains(withoutBrowser, "will meet the same wall") || strings.Contains(withoutBrowser, "may pass") {
		t.Errorf("a wall no browser met changed its retry advice:\n%s", withoutBrowser)
	}
}

// TestChallengeVendorIsNeverReadFromTheHarvestersOwnText: the harvester's own
// challenge message names Cloudflare for every wall (net.go FailureMessage), and
// nothing the site sent — headers, cookies, markup — reaches the public failure
// text, so the text names no vendor: "an access challenge", never a guess.
func TestChallengeVendorIsNeverReadFromTheHarvestersOwnText(t *testing.T) {
	got := PublicFailureMessage(walledResult([]string{"direct", "browser"}))
	if !strings.Contains(got, "The source is behind an access challenge;") {
		t.Errorf("the challenge is not named as an access challenge:\n%s", got)
	}
	for _, vendor := range []string{"Cloudflare", "DataDome", "Akamai", "Imperva", "CAPTCHA"} {
		if strings.Contains(got, vendor) {
			t.Errorf("the failure names %s from the harvester's own words:\n%s", vendor, got)
		}
	}
}
