package harvest

import (
	"net/url"
	"regexp"
	"strings"
)

type IdentifierKind string

const (
	IdentifierNone  IdentifierKind = ""
	IdentifierDOI   IdentifierKind = "doi"
	IdentifierISBN  IdentifierKind = "isbn"
	IdentifierPMID  IdentifierKind = "pmid"
	IdentifierPMCID IdentifierKind = "pmcid"
	IdentifierTitle IdentifierKind = "title"
)

var (
	doiPattern           = regexp.MustCompile(`(?i)10\.\d{4,9}/[-._;()/:A-Z0-9]+`)
	doiHTTPStatusPattern = regexp.MustCompile(`(?i)\bHTTP\s+(\d{3})\b`)
	citationPDFPattern   = regexp.MustCompile(
		`(?is)<meta[^>]+name=["']citation_pdf_url["'][^>]+content=["']([^"']+)["']`,
	)
	citationPDFPatternReversed = regexp.MustCompile(
		`(?is)<meta[^>]+content=["']([^"']+)["'][^>]+name=["']citation_pdf_url["']`,
	)
	citationDOIPattern = regexp.MustCompile(
		`(?is)<meta[^>]+name=["'](?:citation_doi|dc\.identifier|DC\.Identifier)["'][^>]+content=["']([^"']+)["']`,
	)
	citationTitlePattern = regexp.MustCompile(
		`(?is)<meta[^>]+name=["']citation_title["'][^>]+content=["']([^"']+)["']`,
	)
)

// DOIFrom is the DOI text carries, or, when text is an arXiv id on its own,
// that id's arXiv DOI.
func DOIFrom(text string) string {
	m := doiPattern.FindString(text)
	if m == "" {
		if id := ArXivID(text); id != "" {
			return arxivDOI(id)
		}
	}
	return strings.TrimRight(m, ".,;:)>]")
}

// ExtractMetaLinks pulls the citation DOI/PDF/title hints exposed by many
// blocked publisher landing pages. It is pure and never performs a fetch.
func ExtractMetaLinks(pageHTML string) (doi, pdf, title string) {
	if m := citationPDFPattern.FindStringSubmatch(pageHTML); len(m) > 1 {
		pdf = m[1]
	} else if m := citationPDFPatternReversed.FindStringSubmatch(pageHTML); len(m) > 1 {
		pdf = m[1]
	}
	if m := citationDOIPattern.FindStringSubmatch(pageHTML); len(m) > 1 {
		doi = DOIFrom(m[1])
	}
	if m := citationTitlePattern.FindStringSubmatch(pageHTML); len(m) > 1 {
		title = m[1]
	}
	return
}

func NormalizeISBN(s string) string {
	d := strings.ToUpper(strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		if r == 'X' {
			return r
		}
		return -1
	}, s))
	if d != "" && strings.Trim(d, string(d[0])) == "" {
		return "" // reject checksum-valid placeholder values such as 0000000000
	}
	if len(d) == 13 && isbn13Valid(d) {
		return d
	}
	if len(d) == 10 && isbn10Valid(d) {
		return d
	}
	return ""
}

func isbn13Valid(s string) bool {
	if !strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979") {
		return false
	}
	sum := 0
	for i, r := range s {
		v := int(r - '0')
		if i%2 == 1 {
			v *= 3
		}
		sum += v
	}
	return sum%10 == 0
}

func isbn10Valid(s string) bool {
	sum := 0
	for i, r := range s {
		v := 0
		switch {
		case r == 'X' && i == 9:
			v = 10
		case r >= '0' && r <= '9':
			v = int(r - '0')
		default:
			return false
		}
		sum += (10 - i) * v
	}
	return sum%11 == 0
}

// NormalizeIdentifier returns a canonical DOI or checksum-valid ISBN. Other
// identifiers are returned in their canonical uppercase/lowercase form.
func NormalizeIdentifier(input string) string {
	input = strings.TrimSpace(input)
	if doi := DOIFrom(input); doi != "" {
		return doi
	}
	low := strings.ToLower(input)
	if strings.HasPrefix(low, "doi:") {
		return DOIFrom(input[4:])
	}
	if strings.HasPrefix(low, "pmcid:") {
		return strings.ToUpper(strings.TrimSpace(input[6:]))
	}
	if strings.HasPrefix(low, "pmid:") {
		return strings.TrimSpace(input[5:])
	}
	if strings.HasPrefix(low, "isbn:") {
		return NormalizeISBN(input[5:])
	}
	return NormalizeISBN(input)
}

func ClassifyIdentifier(input string) IdentifierKind {
	trim := strings.TrimSpace(input)
	lowInput := strings.ToLower(trim)
	if parsed, err := url.Parse(
		trim,
	); err == nil && (parsed.Scheme == schemeHTTP || parsed.Scheme == schemeHTTPS) &&
		parsed.Host != "" {
		host := strings.ToLower(parsed.Hostname())
		if host != "doi.org" && host != "dx.doi.org" {
			return IdentifierNone
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return IdentifierNone
		}
	}
	if DOIFrom(trim) != "" || strings.HasPrefix(lowInput, "doi:") {
		return IdentifierDOI
	}
	low := strings.ToLower(trim)
	if strings.HasPrefix(low, "pmcid:") {
		trim = strings.TrimSpace(trim[6:])
		low = strings.ToLower(trim)
	}
	if strings.HasPrefix(low, "pmc") && regexp.MustCompile(`^pmc\d+$`).MatchString(low) {
		return IdentifierPMCID
	}
	if strings.HasPrefix(strings.ToLower(trim), "pmid:") {
		trim = strings.TrimSpace(trim[5:])
	}
	if regexp.MustCompile(`^\d{7,9}$`).MatchString(trim) {
		return IdentifierPMID
	}
	if strings.HasPrefix(low, "isbn:") || NormalizeISBN(trim) != "" {
		return IdentifierISBN
	}
	return IdentifierNone
}

// ExtractDOI is the exported spelling used by adapters that only need lexical
// DOI extraction and must not start network discovery.
func ExtractDOI(text string) string { return DOIFrom(text) }

func extractPMCID(raw string) string {
	match := regexp.MustCompile(`(?i)(PMC\d+)`).FindString(raw)
	return strings.ToUpper(match)
}

func doiPrefixOf(doi string) string {
	if i := strings.Index(doi, "/"); i > 0 {
		return doi[:i]
	}
	return ""
}

// arxivIDPattern is an arXiv id standing alone, `arXiv:` prefix optional: the
// new style YYMM.NNNNN and the old style archive[.SC]/YYMMNNN, each with an
// optional vN.
var arxivIDPattern = regexp.MustCompile(
	`(?i)^(?:arxiv:\s*)?(\d{2}(?:0[1-9]|1[0-2])\.\d{4,5}(?:v\d+)?|[a-z]+(?:-[a-z]+)?(?:\.[a-z]{2})?/\d{7}(?:v\d+)?)$`,
)

// arxivURLPattern is an arxiv.org abs or pdf page of one arXiv id.
var arxivURLPattern = regexp.MustCompile(
	`(?i)^https?://(?:www\.)?arxiv\.org/(?:abs|pdf)/(\d{4}\.\d{4,5}(?:v\d+)?|[a-z]+(?:-[a-z]+)?(?:\.[a-z]{2})?/\d{7}(?:v\d+)?)(?:\.pdf)?/?$`,
)

// ArXivID is the arXiv id a source spells on its own ("arXiv:1706.03762",
// "1706.03762v5", "hep-th/9901001"), or "". A URL is never an id here: an
// arxiv.org page in urls reads the page.
func ArXivID(source string) string {
	if match := arxivIDPattern.FindStringSubmatch(strings.TrimSpace(source)); match != nil {
		return match[1]
	}
	return ""
}

// ArXivPageID is the arXiv id an arxiv.org abs or pdf URL names, or "": in
// publications that page names the work.
func ArXivPageID(source string) string {
	if match := arxivURLPattern.FindStringSubmatch(strings.TrimSpace(source)); match != nil {
		return match[1]
	}
	return ""
}

// arxivDOI is an arXiv id's DataCite DOI, which ResolveDOI routes to arXiv.
func arxivDOI(id string) string { return "10.48550/arXiv." + id }
