package harvest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/html"
)

func descendantsByTag(node *html.Node, tag string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && current.Data == tag {
			out = append(out, current)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return out
}

func (r *Resolver) googleScholar(ctx context.Context, query string, limit int) ([]Candidate, error) {
	base := strings.TrimRight(strings.TrimSpace(r.GoogleScholarURL), "/")
	if base == "" {
		return nil, nil
	}
	providerCtx, cancel := providerContext(ctx)
	defer cancel()
	endpoint := base + "/scholar?hl=en&q=" + url.QueryEscape(`"`+query+`"`)
	response, err := r.providerHarvester().providerSearch(providerCtx, endpoint)
	if err != nil {
		return nil, err
	}
	if response.status >= 400 {
		return nil, fmt.Errorf("request to Google Scholar returned HTTP %d", response.status)
	}
	if doiMirrorChallenge(response.body, response.status) {
		return nil, errors.New("response from Google Scholar was a challenge page")
	}
	rows := parseGoogleScholarRows(response.body, limit, "")
	candidates := scholarRowsWithVersions(providerCtx, r.providerHarvester(), response.finalURL, rows, limit, "")
	if len(candidates) == 0 {
		text := strings.ToLower(string(response.body))
		for _, marker := range []string{"did not match any articles", "no results", "no articles"} {
			if strings.Contains(text, marker) {
				return nil, nil
			}
		}
		return nil, errors.New("page from Google Scholar contained no recognizable results")
	}
	return candidates, nil
}

func parseGoogleScholar(body []byte, limit int) []Candidate {
	return parseGoogleScholarFiltered(body, limit, "")
}

func (r *Resolver) googleScholarDOI(ctx context.Context, doi string, limit int) ([]Candidate, error) {
	base := strings.TrimRight(strings.TrimSpace(r.GoogleScholarURL), "/")
	if base == "" {
		return nil, nil
	}
	providerCtx, cancel := providerContext(ctx)
	defer cancel()
	endpoint := base + "/scholar?hl=en&q=" + url.QueryEscape(`"`+doi+`"`)
	h := r.providerHarvester()
	response, err := h.providerGet(providerCtx, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if response.status >= 400 {
		return nil, fmt.Errorf("request to Google Scholar returned HTTP %d", response.status)
	}
	if providerChallenge(response.body, response.status) {
		return nil, errors.New("response from Google Scholar was a challenge page")
	}
	rows := parseGoogleScholarRows(response.body, limit, doi)
	candidates := scholarRowsWithVersions(providerCtx, h, response.finalURL, rows, limit, doi)
	if len(candidates) == 0 {
		return nil, errors.New("response from Google Scholar had no exact DOI result")
	}
	return candidates, nil
}

func (h *Harvester) fetchScholarDOI(ctx context.Context, doi string, options FetchOptions) Result {
	if strings.TrimSpace(h.settings.googleScholarURL) == "" {
		return Result{Source: doi, Error: "Google Scholar is disabled", ErrorKind: errorKindDisabled}
	}
	providerCtx, cancel := providerContext(ctx)
	defer cancel()
	candidates, err := h.resolver().googleScholarDOI(providerCtx, doi, providerCandidateMax)
	if err != nil {
		return providerResult(
			doi,
			sourceGoogleScholar,
			err.Error(),
			errorKind(err),
			0,
			strings.Contains(strings.ToLower(err.Error()), errorKindChallenge),
			[]string{sourceGoogleScholar},
		)
	}
	var last Result
	for _, candidate := range candidates {
		if DOIFrom(candidate.URL) != "" && !strings.EqualFold(DOIFrom(candidate.URL), doi) {
			continue
		}
		result := h.fetchURLWithPolicy(providerCtx, candidate.URL, options, false)
		if result.Error == "" {
			result.Method = sourceGoogleScholar
			result.Rungs = append([]string{sourceGoogleScholar}, result.Rungs...)
			return result
		}
		last = providerResult(
			doi,
			sourceGoogleScholar,
			result.Error,
			result.ErrorKind,
			result.HTTPStatus,
			result.Challenge,
			append([]string{sourceGoogleScholar}, result.Rungs...),
		)
	}
	if last.Error != "" {
		return last
	}
	return providerResult(
		doi,
		sourceGoogleScholar,
		"no exact DOI candidate could be fetched",
		errorKindMissing,
		0,
		false,
		[]string{sourceGoogleScholar},
	)
}

type scholarRow struct {
	candidate   Candidate
	citationURL string
	directPDF   string
	versionsURL string
}

func parseGoogleScholarFiltered(body []byte, limit int, wantedDOI string) []Candidate {
	rows := parseGoogleScholarRows(body, limit, wantedDOI)
	out := make([]Candidate, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		candidate := row.candidate
		candidate.URL = row.citationURL
		if row.directPDF != "" {
			candidate.URL = row.directPDF
		}
		if candidate.URL != "" {
			out = append(out, candidate)
		}
	}
	return out
}

func parseGoogleScholarRows(body []byte, limit int, wantedDOI string) []scholarRow {
	if limit <= 0 || limit > providerCandidateMax {
		limit = providerCandidateMax
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	var rows []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && hasClass(node, "gs_ri") {
			rows = append(rows, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	out := make([]scholarRow, 0, len(rows))
	for _, row := range rows {
		titleNode := firstClass(row, "gs_rt")
		if titleNode == nil {
			continue
		}
		anchor := firstElement(titleNode, "a")
		if anchor == nil {
			continue
		}
		title := strings.TrimSpace(nodeText(titleNode))
		for _, marker := range []string{"[HTML]", "[PDF]", "[BOOK]"} {
			title = strings.TrimSpace(strings.TrimPrefix(title, marker))
		}
		titleLink := nodeAttr(anchor, "href")
		year := 0
		authors := ""
		if summary := firstClass(row, "gs_a"); summary != nil {
			summaryText := strings.TrimSpace(nodeText(summary))
			if match := scholarYearRe.FindString(summaryText); match != "" {
				if _, err := fmt.Sscanf(match, "%d", &year); err != nil {
					fmt.Fprintf(os.Stderr, "harvest: parse Google Scholar year %q: %v\n", match, err)
				}
			}
			authors = summaryText
			if separator := strings.Index(authors, " - "); separator >= 0 {
				authors = strings.TrimSpace(authors[:separator])
			}
		}
		container := row
		for parent := row.Parent; parent != nil; parent = parent.Parent {
			container = parent
			if hasClass(parent, "gs_r") {
				break
			}
		}
		directPDF := ""
		if pdf := firstClass(container, "gs_or_ggsm"); pdf != nil {
			if pdfAnchor := firstElement(pdf, "a"); pdfAnchor != nil {
				pdfLink := nodeAttr(pdfAnchor, "href")
				label := strings.ToLower(nodeText(pdfAnchor))
				if strings.TrimSpace(pdfLink) != "" &&
					(strings.Contains(label, "[pdf]") || strings.Contains(strings.ToLower(pdfLink), ".pdf")) {
					directPDF = pdfLink
				}
			}
		}
		versionsURL := ""
		if versions := firstClass(container, "gs_fl"); versions != nil {
			for _, candidate := range descendantsByTag(versions, "a") {
				label := strings.ToLower(nodeText(candidate))
				if strings.Contains(label, "version") {
					versionsURL = nodeAttr(candidate, "href")
					break
				}
			}
		}
		identityText := title + " " + titleLink + " " + directPDF + " " + nodeText(row)
		if wantedDOI != "" {
			foundDOI := DOIFrom(identityText)
			if !strings.EqualFold(foundDOI, wantedDOI) {
				continue
			}
		}
		if titleLink == "" && directPDF == "" && versionsURL == "" {
			continue
		}
		out = append(
			out,
			scholarRow{
				candidate: Candidate{
					URL:      titleLink,
					Source:   sourceGoogleScholar,
					Priority: 70,
					Kind:     kindPaper,
					Title:    title,
					Authors:  authors,
					Year:     year,
					Match:    .7,
				},
				citationURL: titleLink,
				directPDF:   directPDF,
				versionsURL: versionsURL,
			},
		)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func scholarVersionURL(baseRaw, raw string) (string, bool) {
	base, err := url.Parse(baseRaw)
	if err != nil || base.Host == "" {
		return "", false
	}
	ref, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(ref)
	if !strings.EqualFold(resolved.Host, base.Host) || strings.TrimSuffix(resolved.Path, "/") != "/scholar" ||
		resolved.Query().Get("cluster") == "" {
		return "", false
	}
	resolved.Fragment = ""
	if validateFetchURL(resolved.String(), false) != nil {
		return "", false
	}
	return resolved.String(), true
}

func scholarResourceURL(baseRaw, raw string) string {
	base, err := url.Parse(baseRaw)
	if err != nil || base.Host == "" {
		return ""
	}
	ref, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(ref)
	if resolved.Host == "" || (resolved.Scheme != schemeHTTP && resolved.Scheme != schemeHTTPS) {
		return ""
	}
	resolved.Fragment = ""
	if validateFetchURL(resolved.String(), false) != nil {
		return ""
	}
	return resolved.String()
}

func scholarRowsWithVersions(
	ctx context.Context,
	h *Harvester,
	baseURL string,
	rows []scholarRow,
	limit int,
	wantedDOI string,
) []Candidate {
	if limit <= 0 || limit > providerCandidateMax {
		limit = providerCandidateMax
	}
	out := make([]Candidate, 0, limit)
	versionAttempts := 0
	for i := range rows {
		row := &rows[i]
		if len(out) >= limit {
			break
		}
		candidate := row.candidate
		candidate.URL = scholarResourceURL(baseURL, row.citationURL)
		if row.directPDF != "" {
			if pdfURL := scholarResourceURL(baseURL, row.directPDF); pdfURL != "" {
				candidate.URL = pdfURL
			}
		} else if row.versionsURL != "" && versionAttempts < providerCandidateMax {
			versionAttempts++
			if versionURL, ok := scholarVersionURL(baseURL, row.versionsURL); ok {
				response, err := h.providerGet(ctx, versionURL, nil)
				if err == nil && response.status < 400 && !providerChallenge(response.body, response.status) {
					versions := parseGoogleScholarRows(response.body, providerCandidateMax, wantedDOI)
					for i := range versions {
						version := &versions[i]
						if version.directPDF == "" ||
							titleSimilarity(version.candidate.Title, row.candidate.Title) < .45 {
							continue
						}
						if pdfURL := scholarResourceURL(response.finalURL, version.directPDF); pdfURL != "" {
							candidate = version.candidate
							candidate.URL = pdfURL
							break
						}
					}
				}
			}
		}
		if candidate.URL != "" {
			out = append(out, candidate)
		}
	}
	return out
}

func hasClass(node *html.Node, class string) bool {
	for _, attr := range node.Attr {
		if attr.Key != "class" {
			continue
		}
		for _, value := range strings.Fields(attr.Val) {
			if value == class {
				return true
			}
		}
	}
	return false
}

func firstClass(node *html.Node, class string) *html.Node {
	if hasClass(node, class) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := firstClass(child, class); found != nil {
			return found
		}
	}
	return nil
}

func firstElement(node *html.Node, tag string) *html.Node {
	if node.Type == html.ElementNode && node.Data == tag {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := firstElement(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func nodeText(node *html.Node) string {
	var parts []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			parts = append(parts, current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}
