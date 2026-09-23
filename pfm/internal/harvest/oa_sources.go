package harvest

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

func (r *Resolver) unpaywall(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	var data struct {
		IsOA   bool   `json:"is_oa"`
		Status string `json:"oa_status"`
		Best   struct {
			PDF     string `json:"url_for_pdf"`
			URL     string `json:"url"`
			Version string `json:"version"`
		} `json:"best_oa_location"`
		Locations []struct {
			PDF     string `json:"url_for_pdf"`
			Version string `json:"version"`
		} `json:"oa_locations"`
	}
	// Unpaywall now REQUIRES a real operator email per call (placeholder/example
	// addresses are rejected with HTTP 422 — verified live 2026-08-22). Keyless
	// runs SKIP it cleanly instead of burning a doomed request on every DOI;
	// OpenAlex/S2/EuropePMC cover most of the same OA locations.
	if r.contact() == "" {
		return nil, nil
	}
	if err := getJSON(
		ctx,
		client,
		r.withContact("https://api.unpaywall.org/v2/"+url.PathEscape(doi), "email"),
		&data,
	); err != nil ||
		!data.IsOA {
		return nil, err
	}
	link := data.Best.PDF
	kind := kindPDF
	if link == "" {
		link = data.Best.URL
		kind = kindHTML
	}
	if link == "" {
		return nil, nil
	}
	out := []Candidate{
		{
			URL:      link,
			Source:   sourceUnpaywall,
			Priority: candidatePriority(sourceUnpaywall, data.Status, data.Best.Version, kind),
			Kind:     kind,
			Free:     data.Status,
		},
	}
	for _, location := range data.Locations {
		if location.PDF != "" {
			out = append(
				out,
				Candidate{
					URL:      location.PDF,
					Source:   sourceUnpaywall,
					Priority: candidatePriority(sourceUnpaywall, data.Status, location.Version, kindPDF) + 5,
					Kind:     kindPDF,
					Free:     data.Status,
				},
			)
		}
	}
	return out, nil
}

func (r *Resolver) openAlexDOI(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	type location struct {
		IsOA    bool   `json:"is_oa"`
		PDF     string `json:"pdf_url"`
		Version string `json:"version"`
	}
	var data struct {
		OA struct {
			URL    string `json:"oa_url"`
			Status string `json:"oa_status"`
		} `json:"open_access"`
		Best      *location  `json:"best_oa_location"`
		Locations []location `json:"locations"`
	}
	if err := getJSON(
		ctx,
		client,
		r.withContact("https://api.openalex.org/works/https://doi.org/"+url.PathEscape(doi), "mailto"),
		&data,
	); err != nil {
		return nil, err
	}
	locations := data.Locations
	if data.Best != nil {
		locations = append([]location{*data.Best}, locations...)
	}
	// oa_url is the best copy's PDF when it has one, else its landing page —
	// for 10.1038/nature14539 a HAL record page, abstract and metadata only.
	// It is a PDF only when a location names it as its pdf_url or its path
	// says so; otherwise it is read as the HTML page it is.
	pdfs := map[string]bool{}
	for _, l := range locations {
		if l.IsOA && l.PDF != "" {
			pdfs[l.PDF] = true
		}
	}
	out := []Candidate{}
	if link := data.OA.URL; link != "" {
		kind := kindHTML
		if pdfs[link] || strings.HasSuffix(strings.ToLower(strings.SplitN(link, "?", 2)[0]), ".pdf") {
			kind = kindPDF
		}
		out = append(
			out,
			Candidate{
				URL:      link,
				Source:   sourceOpenAlex,
				Priority: candidatePriority(sourceOpenAlex, data.OA.Status, "", kind),
				Kind:     kind,
				Free:     data.OA.Status,
			},
		)
	}
	seen := map[string]bool{data.OA.URL: true}
	for _, l := range locations {
		if l.IsOA && l.PDF != "" && !seen[l.PDF] {
			seen[l.PDF] = true
			out = append(
				out,
				Candidate{
					URL:      l.PDF,
					Source:   sourceOpenAlex,
					Priority: candidatePriority(sourceOpenAlex, data.OA.Status, l.Version, kindPDF) + 4,
					Kind:     kindPDF,
					Free:     data.OA.Status,
				},
			)
		}
	}
	return out, nil
}

func (r *Resolver) crossref(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	var data struct {
		Message struct {
			Links []struct {
				URL  string `json:"URL"`
				Type string `json:"content-type"`
			} `json:"link"`
		} `json:"message"`
	}
	if err := getJSON(
		ctx,
		client,
		r.withContact("https://api.crossref.org/works/"+url.PathEscape(doi), "mailto"),
		&data,
	); err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, l := range data.Message.Links {
		if l.URL != "" && strings.Contains(l.Type, kindPDF) {
			out = append(out, Candidate{URL: l.URL, Source: sourceCrossref, Priority: 30, Kind: kindPDF})
		}
	}
	return out, nil
}

func (r *Resolver) semanticScholar(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	var data struct {
		PDF *struct {
			URL    string `json:"url"`
			Status string `json:"status"`
		} `json:"openAccessPdf"`
		External map[string]string `json:"externalIds"`
	}
	headers := map[string]string{}
	if key := strings.TrimSpace(r.SemanticScholarAPIKey); key != "" {
		headers["x-api-key"] = key
	}
	if err := getJSONWithHeaders(
		ctx,
		client,
		"https://api.semanticscholar.org/graph/v1/paper/DOI:"+url.PathEscape(doi)+"?fields=openAccessPdf,externalIds",
		headers,
		&data,
	); err != nil {
		return nil, err
	}
	out := []Candidate{}
	if data.PDF != nil && data.PDF.URL != "" {
		out = append(
			out,
			Candidate{
				URL:      data.PDF.URL,
				Source:   sourceSemanticScholar,
				Priority: candidatePriority(sourceSemanticScholar, data.PDF.Status, "", kindPDF),
				Kind:     kindPDF,
				Free:     data.PDF.Status,
			},
		)
	}
	if arxiv := data.External["ArXiv"]; arxiv != "" {
		out = append(
			out,
			Candidate{URL: "https://arxiv.org/pdf/" + arxiv, Source: sourceArXiv, Priority: 0, Kind: kindPDF},
		)
	}
	if pmc := data.External["PubMedCentral"]; pmc != "" {
		if !strings.HasPrefix(strings.ToUpper(pmc), "PMC") {
			pmc = "PMC" + pmc
		}
		out = append(
			out,
			Candidate{
				URL:      "https://europepmc.org/articles/" + pmc + "?pdf=render",
				Source:   sourceEuropePMC,
				Priority: 16,
				Kind:     kindPDF,
			},
		)
	}
	return out, nil
}

func (r *Resolver) core(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	var data struct {
		Download string   `json:"downloadUrl"`
		Sources  []string `json:"sourceFulltextUrls"`
	}
	u := "https://api.core.ac.uk/v3/works/" + url.PathEscape(doi)
	// Through the fetch gateway, like every other harvester egress — which also
	// bounds a response this call previously decoded straight off the socket
	// with no ceiling. The API key rides a header, so this request must never
	// escalate to a browser rung that would render it somewhere else.
	headers := map[string]string{}
	if key := strings.TrimSpace(r.CoreAPIKey); key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	// getJSONWithHeaders, not getBodyWithHeaders: a JSON-decoding path refuses
	// an over-ceiling body by name rather than truncating it into a decode
	// failure.
	if err := getJSONWithHeaders(ctx, client, u, headers, &data); err != nil {
		return nil, err
	}
	out := []Candidate{}
	if data.Download != "" {
		out = append(out, Candidate{URL: data.Download, Source: sourceCORE, Priority: 40, Kind: kindPDF})
	}
	for _, s := range data.Sources {
		if s != "" {
			out = append(out, Candidate{URL: s, Source: sourceCORE, Priority: 41, Kind: kindPDF})
		}
	}
	return out, nil
}

func (r *Resolver) doaj(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	var data struct {
		Results []struct {
			Bib struct {
				Links []struct {
					URL  string `json:"url"`
					Type string `json:"type"`
				} `json:"link"`
			} `json:"bibjson"`
		} `json:"results"`
	}
	if err := getJSON(ctx, client, "https://doaj.org/api/search/articles/doi:"+url.PathEscape(doi), &data); err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, row := range data.Results[:min(len(data.Results), 1)] {
		for _, link := range row.Bib.Links {
			if link.URL != "" && link.Type == "fulltext" {
				out = append(
					out,
					Candidate{
						URL:      link.URL,
						Source:   sourceDOAJ,
						Priority: candidatePriority(sourceDOAJ, "", "", kindHTML),
						Kind:     kindHTML,
					},
				)
			}
		}
	}
	return out, nil
}

func (r *Resolver) europePMCDOI(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	pmcid, err := idToPMCID(ctx, client, doi, r)
	if err != nil || pmcid == "" {
		return nil, err
	}
	return []Candidate{
		{
			URL:      "https://europepmc.org/articles/" + pmcid + "?pdf=render",
			Source:   sourceEuropePMC,
			Priority: candidatePriority(sourceEuropePMC, accessGreen, "", kindPDF),
			Kind:     kindPDF,
			Free:     accessGreen,
		},
	}, nil
}

// ── wave additions: OpenAIRE / Zenodo / eLife / PLOS / NBER / HathiTrust ──────

var (
	plosJournalCodeRe  = regexp.MustCompile(`(?i)^10\.1371/journal\.([a-z]+)\.`)
	nberWorkingPaperRe = regexp.MustCompile(`^w\d+$`)
)

// openAIRE resolves a DOI to every repository instance's full-text URL via the
// EU aggregator. Endpoint verified live 2026-08-22; keyless. The JSON shape of
// instances varies by record version, so webresource URLs are collected with an
// ITERATIVE walk (a recursive walk blew the stack on deeply nested input).
func (r *Resolver) openAIRE(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	var data interface{}
	if err := getJSON(
		ctx,
		client,
		"https://api.openaire.eu/search/publications?doi="+url.QueryEscape(doi)+"&format=json",
		&data,
	); err != nil {
		return nil, err
	}
	out := []Candidate{}
	seen := map[string]bool{}
	stack := []interface{}{data}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch v := cur.(type) {
		case map[string]interface{}:
			if wr, ok := v["webresource"]; ok {
				out = appendOpenAireResources(out, seen, wr)
			}
			for _, child := range v {
				stack = append(stack, child)
			}
		case []interface{}:
			stack = append(stack, v...)
		}
	}
	// Stable: the walk order over a JSON map is already arbitrary, so a
	// non-stable sort would let equal-priority instances swap between runs.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, nil
}

func appendOpenAireResources(out []Candidate, seen map[string]bool, wr interface{}) []Candidate {
	items, ok := wr.([]interface{})
	if !ok {
		items = []interface{}{wr}
	}
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		raw, ok := m["url"]
		if !ok {
			continue
		}
		link := ""
		switch u := raw.(type) {
		case string:
			link = u
		case map[string]interface{}:
			if s, ok := u["$"].(string); ok {
				link = s
			}
		}
		if link == "" || !strings.HasPrefix(link, schemeHTTP) || seen[link] {
			continue
		}
		seen[link] = true
		kind := kindHTML
		if strings.HasSuffix(strings.ToLower(link), ".pdf") {
			kind = kindPDF
		}
		out = append(
			out,
			Candidate{
				URL:      link,
				Source:   sourceOpenAIRE,
				Priority: candidatePriority(sourceOpenAIRE, "", "", kind),
				Kind:     kind,
				Free:     accessGreen,
			},
		)
	}
	return out
}

// zenodo resolves a DOI to the matching record's document files (pdf/epub only
// — an exact-DOI match never turns a .zip dataset into article text).
func (r *Resolver) zenodo(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	var data struct {
		Hits struct {
			Hits []struct {
				DOI   string `json:"doi"`
				Files []struct {
					Key   string `json:"key"`
					Links struct {
						Self string `json:"self"`
					} `json:"links"`
				} `json:"files"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := getJSON(
		ctx,
		client,
		"https://zenodo.org/api/records?q=doi:"+url.QueryEscape(doi)+"&size=3&sort=mostrecent",
		&data,
	); err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, hit := range data.Hits.Hits {
		recDOI := DOIFrom(hit.DOI)
		if recDOI == "" || !strings.EqualFold(recDOI, doi) {
			continue // only THE record for THIS doi — never a neighboring record's files
		}
		for _, file := range hit.Files {
			low := strings.ToLower(file.Key)
			url2 := strings.ToLower(file.Links.Self)
			if file.Links.Self == "" ||
				(!strings.HasSuffix(low, ".pdf") && !strings.HasSuffix(low, ".epub") && !strings.HasSuffix(url2, ".pdf") && !strings.HasSuffix(url2, ".epub")) {
				continue
			}
			kind := kindPDF
			if strings.HasSuffix(low, ".epub") {
				kind = kindEPUB
			}
			out = append(
				out,
				Candidate{
					URL:      file.Links.Self,
					Source:   sourceZenodo,
					Priority: candidatePriority(sourceZenodo, "", "", kind),
					Kind:     kind,
					Free:     accessGreen,
				},
			)
		}
	}
	return out, nil
}

// eLife resolves its DOIs (10.7554/…) through the keyless articles API whose
// items carry a direct CDN PDF (verified live 2026-08-22).
func (r *Resolver) eLife(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	if doiPrefixOf(doi) != "10.7554" {
		return nil, nil
	}
	var data struct {
		Items []struct {
			PDF string `json:"pdf"`
		} `json:"items"`
	}
	if err := getJSON(
		ctx,
		client,
		"https://api.elifesciences.org/articles?by-doi="+url.QueryEscape(doi),
		&data,
	); err != nil {
		return nil, err
	}
	if len(data.Items) == 0 || data.Items[0].PDF == "" {
		return nil, nil
	}
	return []Candidate{
		{
			URL:      data.Items[0].PDF,
			Source:   sourceELife,
			Priority: candidatePriority(sourceELife, "", "", kindPDF),
			Kind:     kindPDF,
			Free:     accessGold,
		},
	}, nil
}

// plosCandidates derives the printable-PDF URL offline from the DOI's journal
// code (verified live 2026-08-22: journals.plos.org/{code}/article/file?id={doi}
// &type=printable serves application/pdf to a plain UA). No API call needed.
func plosCandidates(doi string) []Candidate {
	matched := plosJournalCodeRe.FindStringSubmatch(doi)
	if matched == nil {
		return nil
	}
	target := "https://journals.plos.org/" + strings.ToLower(
		matched[1],
	) + "/article/file?id=" + url.QueryEscape(
		doi,
	) + "&type=printable"
	return []Candidate{
		{
			URL:      target,
			Source:   sourcePLOS,
			Priority: candidatePriority(sourcePLOS, "", "", kindPDF),
			Kind:     kindPDF,
			Free:     accessGold,
		},
	}
}

// nberCandidates derives the free working-paper PDF offline from the
// 10.3386/w{id} DOI (verified live 2026-08-22). No API call needed.
func nberCandidates(doi string) []Candidate {
	if doiPrefixOf(doi) != "10.3386" {
		return nil
	}
	wp := strings.ToLower(doi[strings.Index(doi, "/")+1:])
	if !nberWorkingPaperRe.MatchString(wp) {
		return nil
	}
	target := "https://www.nber.org/system/files/working_papers/" + wp + "/" + wp + ".pdf"
	return []Candidate{
		{
			URL:      target,
			Source:   sourceNBER,
			Priority: candidatePriority(sourceNBER, "", "", kindPDF),
			Kind:     kindPDF,
			Free:     accessGreen,
		},
	}
}
