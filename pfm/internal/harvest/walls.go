package harvest

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The partials a walled page is stored under: what was read, and why no more.
const (
	paywallReason   = "paywalled: only the preview the site serves without a subscription was read"
	loginWallReason = "login wall: only what the site shows signed-out was read"
)

// loginGateThinChars is the HTTP ladder's thin-page floor (harvest.go): a
// login-walled page converting to less than this holds nothing but its gate.
const loginGateThinChars = 500

var (
	// accessibleForFreeFalse is schema.org's paywall flag in a JSON-LD block,
	// on the article or on one of its hasPart elements.
	accessibleForFreeFalse = regexp.MustCompile(`(?i)"isAccessibleForFree"\s*:\s*(?:false|"false")`)
	// paywallContainerPattern names a paywall element's id, class or test id.
	paywallContainerPattern = regexp.MustCompile(`(?i)paywall|pay-wall|regwall|reg-wall|gateway|meter-?wall|tp-modal`)
	// paywallCallPattern is the subscription call a paywall element carries.
	paywallCallPattern = regexp.MustCompile(`(?i)subscri|sign in|log in|unlimited access`)
	// loginGateContainerPattern names a logged-out app's sign-in gate.
	loginGateContainerPattern = regexp.MustCompile(
		`(?i)^(loginbutton|signupbutton|bottombar)$|authwall|login[-_]?form|sign-?in-?modal|login-?modal`,
	)
	// loginGatePath is the sign-in or sign-up address a gate links
	// (LinkedIn's legacy directory pages link /reg/join-context).
	loginGatePath = regexp.MustCompile(
		`^/(login|signup|i/flow/(login|signup)|i/jf/onboarding|accounts/login|login\.php|uas/login|reg/join[a-z-]*)(/|$|\?)`,
	)
)

// loginWalledHosts are the app sites that show a signed-out reader a sign-in gate.
var loginWalledHosts = []string{"x.com", "twitter.com", "instagram.com", "facebook.com", "linkedin.com"}

// pageWall names the wall a page's markup shows — a paywall or, on a
// logged-out app site, a login gate — or "" when its markup shows none.
// Prose alone never counts: a paywall is schema.org's isAccessibleForFree
// false, or a subscription call inside a paywall element; a login wall is a
// gate element or a sign-in link on an app site. On an app site the paywall
// markup (LinkedIn's isAccessibleForFree false over a post's details) marks
// what a signed-out reader is not shown: its login wall, never a subscription.
func pageWall(source string, doc *html.Node) string {
	if paywalled(doc) {
		if loginWalledHost(source) {
			return loginWallReason
		}
		return paywallReason
	}
	if loginWalledHost(source) && hasNode(doc, loginGate) {
		return loginWallReason
	}
	return ""
}

func paywalled(doc *html.Node) bool {
	return hasNode(doc, func(node *html.Node) bool {
		if node.Data == "script" {
			return strings.EqualFold(strings.TrimSpace(nodeAttr(node, "type")), "application/ld+json") &&
				accessibleForFreeFalse.MatchString(nodeText(node))
		}
		if strings.EqualFold(nodeAttr(node, "itemprop"), "isAccessibleForFree") {
			return strings.EqualFold(strings.TrimSpace(nodeAttr(node, "content")), "false")
		}
		return wallContainer(node, paywallContainerPattern) && paywallCallPattern.MatchString(nodeText(node))
	})
}

func loginGate(node *html.Node) bool {
	if node.Data == "a" {
		if link, err := url.Parse(nodeAttr(node, "href")); err == nil && loginGatePath.MatchString(link.Path) {
			return true
		}
	}
	return wallContainer(node, loginGateContainerPattern)
}

func loginWalledHost(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil {
		obs.Logger(context.Background()).
			Warn("harvest: the address could not be parsed for its host", obs.FieldErr, err.Error())
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	for _, walled := range loginWalledHosts {
		if host == walled || strings.HasSuffix(host, "."+walled) {
			return true
		}
	}
	return false
}

// wallContainer reports whether node's id, class or test id matches pattern.
func wallContainer(node *html.Node, pattern *regexp.Regexp) bool {
	if node.Data == "body" || node.Data == "html" {
		return false
	}
	for _, attr := range node.Attr {
		if (attr.Key == "id" || attr.Key == "class" || attr.Key == "data-testid") &&
			pattern.MatchString(strings.TrimSpace(attr.Val)) {
			return true
		}
	}
	return false
}

func hasNode(doc *html.Node, match func(*html.Node) bool) bool {
	if doc.Type == html.ElementNode && match(doc) {
		return true
	}
	for child := doc.FirstChild; child != nil; child = child.NextSibling {
		if hasNode(child, match) {
			return true
		}
	}
	return false
}

// gateOnly reports whether converted, a page wall named, holds nothing but a
// login gate, and records it on budget so the fetch's failure names it.
func (budget *loaderBudget) gateOnly(wall, converted string) bool {
	if wall != loginWallReason || contentChars(converted) >= loginGateThinChars {
		return false
	}
	if budget != nil {
		budget.loginGateOnly = true
	}
	return true
}

// readerGateOnly reports whether markdown, a reader rung's copy of source,
// is the site's sign-up wall in place of the page (linkedInSignUpWall), and
// records it on budget so the fetch's failure names the wall.
func (budget *loaderBudget) readerGateOnly(source, markdown string) bool {
	if !linkedInSignUpWall(source, markdown) {
		return false
	}
	if budget != nil {
		budget.loginGateOnly = true
	}
	return true
}

// loginWallNote leads a fetch's failure message with the login wall a page
// of it held nothing but, so a gate is never reported as a thin page.
func (budget *loaderBudget) loginWallNote(source, message string) string {
	if budget == nil || !budget.loginGateOnly {
		return message
	}
	return source + " shows nothing but a login wall signed-out: no content is visible without an account. " + message
}
