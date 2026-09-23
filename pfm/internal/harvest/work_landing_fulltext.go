package harvest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// A work's landing page is the answer only when it carries the full text. A
// publisher's landing page read cleanly is most often its abstract page: the
// abstract, the reference list, author and rights boxes and links to similar
// articles, under headings no article body uses. Measured in the sim
// (2026-09): nature.com's page for 10.1038/nature14539 has 24,825 characters
// and 17 headings, none of them a body section (the 20,078-character
// reference list is most of it); link.springer.com's page for
// 10.1007/BF00994018 has 5,272 characters and 13 headings, none a body
// section; science.org's page for 10.1126/science.1127647 has 27,648
// characters under one heading, its "Register and access" box; hal.science's
// record page for the same Nature paper has 12,704 characters and 5 sections
// past 200 characters of prose — its search form, author list, domains, cite
// and export boxes — and no body, so those count as apparatus. A full-text
// article carries its body under several section headings — Introduction,
// Methods, Results, Discussion — each with prose.
const (
	// landingBodySections is the fewest body sections a full-text landing
	// page carries; the measured abstract pages carry 0 and 1.
	landingBodySections = 3
	// landingBodyChars is the least prose under body sections a full-text
	// landing page carries; an abstract alone is about 1,000 characters.
	landingBodyChars = 5000
	// landingSectionChars is the least prose a heading needs to count as a
	// body section rather than a label.
	landingSectionChars = 200
)

// landingBoilerplate are the headings of a landing page's apparatus, never of
// an article body, matched on the lowered heading text's start.
var landingBoilerplate = []string{
	"abstract", "summary", "editor's summary", "structured abstract", "graphical abstract", "highlights",
	"references", "bibliography", "literature cited", "acknowledgement", "acknowledgment",
	"author information", "authors and affiliations", "corresponding author", "author contributions",
	"ethics declarations", "competing interests", "conflict of interest", "conflicts of interest",
	"additional information", "rights and permissions", "permissions", "about this article",
	"cite", "export", "keywords", "access options", "similar content", "related articles",
	"recommended", "article pdf", "supplementary", "data availability", "code availability",
	"funding", "footnotes", "notes", "metrics", "citations", "cited by", "share", "figures", "tables",
	"register", "access", "sign in", "log in", "subscribe", "purchase", "buy", "get access",
	"advanced search", "search using", "domains", "dates and versions", "identifiers", "licence", "license",
	"altmetric",
}

var (
	landingHeadingPattern = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	landingNumberPattern  = regexp.MustCompile(`^[\divxlc]+[.)]?\s+`)
)

// landingFullText reports whether a landing page's Markdown carries the full
// text, and the measure that decided it: the prose characters and the count
// of body sections, apparatus and link headings not counted.
func landingFullText(content string) (bool, string) {
	sections, chars := 0, 0
	body, prose := false, 0
	closeSection := func() {
		if body && prose >= landingSectionChars {
			sections++
			chars += prose
		}
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if match := landingHeadingPattern.FindStringSubmatch(line); match != nil {
			closeSection()
			body, prose = landingBodyHeading(match[1]), 0
			continue
		}
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "|") || strings.HasPrefix(line, ">") {
			continue
		}
		prose += len(line)
	}
	closeSection()
	measure := fmt.Sprintf("%d body sections, %d characters of body text", sections, chars)
	return sections >= landingBodySections && chars >= landingBodyChars, measure
}

func landingBodyHeading(heading string) bool {
	heading = strings.ToLower(strings.TrimSpace(strings.Trim(heading, "*_ ")))
	if heading == "" || strings.HasPrefix(heading, "[") {
		return false
	}
	heading = landingNumberPattern.ReplaceAllString(heading, "")
	for _, apparatus := range landingBoilerplate {
		if strings.HasPrefix(heading, apparatus) {
			return false
		}
	}
	return true
}

// pageLinkPattern matches a Markdown link's target: `[text](target)`, the
// target optionally in angle brackets and followed by a quoted title.
var pageLinkPattern = regexp.MustCompile(`\]\(\s*<?([^\s()<>]+)>?(?:\s+"[^"]*")?\s*\)`)

// pageFullTextLink is the page's own link to its full text: the first
// Markdown link in content that resolves, against pageURL, to the page's
// origin with a path ending in .pdf; "" when the page carries none. An
// open-access record page (hal.science's for 10.1038/nature14539) carries
// the abstract and links its PDF this way.
func pageFullTextLink(content, pageURL string) string {
	base, err := url.Parse(pageURL)
	origin := webOrigin(pageURL)
	if err != nil || origin == "" {
		return ""
	}
	for _, match := range pageLinkPattern.FindAllStringSubmatch(content, -1) {
		ref, refErr := url.Parse(match[1])
		if refErr != nil {
			continue
		}
		target := base.ResolveReference(ref)
		target.Fragment = ""
		if webOrigin(target.String()) == origin && strings.HasSuffix(strings.ToLower(target.Path), ".pdf") {
			return target.String()
		}
	}
	return ""
}

// readPageFullTextLink reads a thin open-access page's own PDF link
// (pageFullTextLink) through the fetch path; ok only when it reads as a PDF.
func (h *Harvester) readPageFullTextLink(
	ctx context.Context,
	pageURL, content string,
	options FetchOptions,
) (Result, bool) {
	link := pageFullTextLink(content, pageURL)
	if link == "" {
		return Result{}, false
	}
	linked := h.fetchURLWithPolicy(ctx, link, options, false)
	if linked.Error != "" || linked.Kind != kindPDF {
		obs.Logger(ctx).Info("harvest: the open-access page's PDF link did not read as a PDF",
			"page", pageURL, "link", link, "kind", linked.Kind, "err", linked.Error)
		return Result{}, false
	}
	noteServed(ctx, link)
	return linked, true
}

// thinCopy is the first open-access page read cleanly that carried no full
// text, kept for when no other candidate carries it.
type thinCopy struct {
	result  Result
	url     string
	measure string
	trace   []string
}

// storeThinCopy stores the kept thin page as the work's answer, its partial
// naming why: the page carries no full text and no other candidate did. A
// partial the page already carried stays first.
func (h *Harvester) storeThinCopy(
	ctx context.Context,
	source, canonical string,
	thin thinCopy,
	options FetchOptions,
) Result {
	result, content := thin.result, thin.result.Content
	if result.Path != "" {
		raw, err := os.ReadFile(result.Path)
		if err != nil {
			obs.Logger(ctx).Info(
				"harvest: the thin open-access page's artifact did not read; its inline content stands",
				"path", result.Path, "err", err,
			)
		} else {
			_, content = parseCacheFrontmatter(string(raw))
		}
	}
	reason := "the open-access copy at " + webOrigin(thin.url) + " carries no full text (" + thin.measure +
		"), and no other candidate carried full text"
	if prior := partialReason(content); prior != "" {
		reason, content = prior+"; "+reason, partialBody(content)
	}
	result.Content, result.Path = withPartial(content, reason), ""
	noteServed(ctx, thin.url)
	return h.storeResultAlias(source, canonical, result, thin.trace, options)
}
