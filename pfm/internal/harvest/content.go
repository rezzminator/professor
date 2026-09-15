package harvest

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

func (h *Harvester) convert(ctx context.Context, kind, source string, body []byte) (string, error) {
	if kind == "txt" {
		return string(body), nil
	}
	if h.options.Converter == nil {
		return "", errors.New("no injected converter configured for " + kind)
	}
	return h.options.Converter.Convert(ctx, kind, source, body)
}

func classifyFetchedKind(source, contentType string, body []byte) string {
	kind := classifyKind(source, contentType, body)
	if kind != "html" && kind != "txt" {
		return kind
	}
	if IsPlainText(source, contentType, string(body)) {
		return "txt"
	}
	return "html"
}

func usableContent(content, kind string) bool {
	if content == "" {
		return false
	}
	if kind == "html" || kind == "txt" {
		return len(strings.TrimSpace(content)) >= 1
	}
	return true
}

func isBibliographicLanding(content string) bool {
	if !hasTextHeading(content, "abstract") {
		return false
	}
	metadata := hasTextHeading(content, "fingerprint") || hasTextHeading(content, "cite this") ||
		strings.Contains(strings.ToLower(content), "research output")
	if !metadata {
		return false
	}
	for _, heading := range []string{"introduction", "background", "methods", "materials and methods", "methods and materials", "results", "discussion", "conclusion", "references"} {
		if hasTextHeading(content, heading) {
			return false
		}
	}
	return true
}

func hasTextHeading(content, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#* ")
		line = strings.TrimSpace(strings.Trim(line, ":"))
		if strings.EqualFold(line, want) {
			return true
		}
	}
	return false
}

type bibliographicLandingHopKey struct{}

func bibliographicDocumentURL(body []byte, baseRaw string) string {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return ""
	}
	var found string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != "" {
			return
		}
		if node.Type == html.ElementNode && node.Data == "a" {
			href := strings.TrimSpace(nodeAttr(node, "href"))
			if href != "" {
				class := strings.ToLower(nodeAttr(node, "class"))
				label := strings.ToLower(strings.TrimSpace(nodeText(node)))
				parsed, parseErr := url.Parse(href)
				if parseErr == nil {
					path := strings.ToLower(parsed.Path)
					extension := filepath.Ext(path)
					isDocument := strings.Contains(class, "document-link") || strings.Contains(path, "/files/") ||
						strings.Contains(label, "full text") ||
						strings.Contains(label, "manuscript")
					supported := extension == ".pdf" || extension == ".doc" || extension == ".docx" ||
						extension == ".epub" ||
						extension == ".odt" ||
						extension == ".rtf" ||
						extension == ".txt"
					if isDocument && (supported || strings.Contains(class, "document-link")) {
						base, baseErr := url.Parse(baseRaw)
						if baseErr == nil {
							resolved := base.ResolveReference(parsed)
							resolved.Fragment = ""
							if (resolved.Scheme == "http" || resolved.Scheme == "https") &&
								assertFetchable(resolved.String(), false) == nil {
								found = resolved.String()
							}
						}
					}
				}
			}
		}
		for child := node.FirstChild; child != nil && found == ""; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

func contentChars(content string) int { return len([]rune(strings.TrimSpace(content))) }

// blankRenderPage reports whether a rendered document carries no visible
// text. Chrome serialises at least <html><head></head><body></body></html>
// for any navigated page — the raw string is never empty, so emptiness is
// judged on tag-stripped text: script/style bodies dropped, tags removed,
// and effectively nothing left. BLANK is not THIN: a short-but-real page
// must fall through to the 500-char acceptance floor instead, where it
// reads as "ran and could not pass", never as an empty render.
var (
	scriptRe  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRe   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)
)

func blankRenderPage(html string) bool {
	text := scriptRe.ReplaceAllString(html, " ")
	text = styleRe.ReplaceAllString(text, " ")
	text = htmlTagRe.ReplaceAllString(text, " ")
	return contentChars(text) < 10
}

func kindFromName(source string) string {
	return DetectKind(source)
}
