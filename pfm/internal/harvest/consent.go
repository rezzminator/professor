package harvest

import (
	"bytes"
	"context"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// consentContainerPattern names the id or class of a consent manager's
// container (OneTrust, Didomi, Quantcast/TCF, Cookiebot, Usercentrics,
// TrustArc, Sourcepoint, and a generic cookie/consent banner). Their vendor
// lists name Cloudflare, reCAPTCHA or Turnstile as cookie providers, which a
// challenge gate would otherwise read as a bot wall.
var consentContainerPattern = regexp.MustCompile(
	`(?i)onetrust|didomi|qc-cmp|cookiebot|usercentrics|truste[-_]|trustarc|sp_message|` +
		`\bcmp\b|cmp-|consent|cookie-?banner|cookie-?notice|cookie-?law|gdpr`,
)

// withoutConsentMarkup returns body with every consent-manager container
// removed, so a banner's vendor list never decides a challenge verdict. A body
// that does not parse is returned unchanged, with the reason logged.
func withoutConsentMarkup(body []byte) []byte {
	if !consentContainerPattern.Match(body) {
		return body
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		obs.Logger(context.Background()).Warn(
			"harvest: consent markup kept, the page did not parse",
			obs.FieldErr,
			err.Error(),
		)
		return body
	}
	var drop []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && consentContainer(node) {
			drop = append(drop, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if len(drop) == 0 {
		return body
	}
	for _, node := range drop {
		node.Parent.RemoveChild(node)
	}
	var out bytes.Buffer
	if err := html.Render(&out, root); err != nil {
		obs.Logger(context.Background()).Warn(
			"harvest: consent markup kept, the page did not render back",
			obs.FieldErr,
			err.Error(),
		)
		return body
	}
	return out.Bytes()
}

func consentContainer(node *html.Node) bool {
	if node.Data == "body" || node.Data == "html" {
		return false
	}
	for _, attr := range node.Attr {
		if (attr.Key == "id" || attr.Key == "class") &&
			consentContainerPattern.MatchString(strings.TrimSpace(attr.Val)) {
			return true
		}
	}
	return false
}
