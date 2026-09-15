package harvest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// An app shell is the static HTML a client-rendered site serves for EVERY
// route: a bundle, a mount point, and often a no-JS explainer long enough to
// clear the 500-char thin-page floor. The requested route's own content only
// exists after the bundle runs, so accepting the shell stores the SAME page
// under every URL on the host. The proof is route-independence: a sibling
// path that cannot exist returns the same visible text.

// appShellProbePrefix names the sibling path the probe requests.
const appShellProbePrefix = "harvester-shell-probe-"

// clientAppMarkers gate the probe: a page with none of them never pays for
// the extra request. They are cheap hints, not a verdict — an SSR page that
// ships a module bundle passes the gate and is cleared by the probe itself.
var clientAppMarkers = []string{
	`type="module"`, `type=module`, `id="root"`, `id="app"`, `id="__next"`,
	`id="__nuxt"`, `id="___gatsby"`, `id="svelte"`, `<app-root`, `data-reactroot`,
}

var (
	noscriptRe  = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
	commentRe   = regexp.MustCompile(`(?s)<!--.*?-->`)
	shellWordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)
)

// appShellKey carries a detected shell's text into the Wayback recursion, so
// a snapshot of the same shell is rejected before it is stored.
type appShellKey struct{}

func looksLikeClientApp(body []byte) bool {
	low := strings.ToLower(string(body))
	if !strings.Contains(low, "<script") {
		return false
	}
	for _, marker := range clientAppMarkers {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

// appShellProbeURL returns a sibling of source that no real site serves: same
// scheme and host, same parent directory, a random final segment, and no
// query or fragment.
func appShellProbeURL(source string) (string, bool) {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	dir := parsed.Path
	if !strings.HasSuffix(dir, "/") {
		dir = path.Dir(dir)
		if dir == "." {
			dir = "/"
		}
		if !strings.HasSuffix(dir, "/") {
			dir += "/"
		}
	}
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		log.Printf("harvest: app-shell probe token for %s: %v", source, err)
		return "", false
	}
	probe := &url.URL{
		Scheme: parsed.Scheme,
		Host:   parsed.Host,
		Path:   dir + appShellProbePrefix + hex.EncodeToString(token),
	}
	return probe.String(), true
}

// shellFingerprint is a document's visible text: script, style, noscript and
// comment bodies dropped, tags removed, whitespace collapsed. Attribute-level
// noise (nonces, build hashes) never reaches it.
func shellFingerprint(body []byte) string {
	text := commentRe.ReplaceAllString(string(body), " ")
	text = scriptRe.ReplaceAllString(text, " ")
	text = styleRe.ReplaceAllString(text, " ")
	text = noscriptRe.ReplaceAllString(text, " ")
	text = htmlTagRe.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

// probeAppShell reports whether source's page is route-independent: a sibling
// that cannot exist returns the same visible text. A probe that could not run
// or hit a wall answers false and logs why — the page is then judged exactly
// as it was before the probe existed.
func (h *Harvester) probeAppShell(ctx context.Context, client *http.Client, ua, source string, body []byte) bool {
	want := shellFingerprint(body)
	if want == "" {
		return false
	}
	probe, ok := appShellProbeURL(source)
	if !ok {
		return false
	}
	probeBody, status, _, err := getBody(ctx, client, probe, ua, h.options.MaxBytes)
	if err != nil {
		log.Printf("harvest: app-shell probe %s for %s could not run: %v", probe, source, err)
		return false
	}
	if len(probeBody) == 0 || isChallenge(probeBody, status) {
		log.Printf(
			"harvest: app-shell probe %s for %s returned no comparable page (HTTP %d, %d bytes)",
			probe,
			source,
			status,
			len(probeBody),
		)
		return false
	}
	return shellFingerprint(probeBody) == want
}

// sameAsShell reports whether a later rung's content is still the shell:
// at least 80% of its distinct words already appear in the shell's text.
// Readers re-wrap the shell (frontmatter, markdown syntax, raw HTML, a
// longer render of the same explainer), so both sides are reduced to visible
// text first, and neither length nor exact equality decides; a real render of
// the route brings its own vocabulary.
func sameAsShell(shell, content string) bool {
	if shell == "" {
		return false
	}
	shellWords := map[string]struct{}{}
	for _, word := range shellWordRe.FindAllString(strings.ToLower(shellFingerprint([]byte(shell))), -1) {
		shellWords[word] = struct{}{}
	}
	words := map[string]struct{}{}
	for _, word := range shellWordRe.FindAllString(strings.ToLower(shellFingerprint([]byte(content))), -1) {
		words[word] = struct{}{}
	}
	if len(words) == 0 {
		return false
	}
	shared := 0
	for word := range words {
		if _, ok := shellWords[word]; ok {
			shared++
		}
	}
	return shared*5 >= len(words)*4
}
