package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Candidate struct {
	URL      string  `json:"url"`
	Source   string  `json:"source,omitempty"`
	Priority int     `json:"priority,omitempty"`
	Kind     string  `json:"kind,omitempty"`
	Title    string  `json:"title,omitempty"`
	Authors  string  `json:"authors,omitempty"`
	Year     int     `json:"year,omitempty"`
	Free     string  `json:"free,omitempty"`
	Match    float64 `json:"match,omitempty"`
}

// Resolver is the legal scholarly metadata/OA resolver. It never downloads
// article bodies; candidates are sent through Harvester's normal transport.
// The optional settings are copied by New at process/server start so provider
// behavior cannot change halfway through one MCP process when its environment
// is edited by a caller.
type Resolver struct {
	Client                *http.Client
	ContactEmail          string
	GoogleBooksAPIKey     string
	CoreAPIKey            string
	SemanticScholarAPIKey string
	IPFSCatalogURL        string
	DOIViewerURL          string
	MD5CatalogURL         string
	GoogleScholarURL      string
}

type doabMetadata struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type resolverUAContextKey struct{}

func resolverContext(ctx context.Context, r *Resolver) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, resolverUAContextKey{}, r.scholarlyUA())
}

func candidatePriority(source, status, version, kind string) int {
	bases := map[string]int{
		sourceArXiv:           0,
		sourceOSF:             2,
		"citation_pdf_url":    4,
		sourceUnpaywall:       10,
		sourceOpenAlex:        12,
		sourceSemanticScholar: 14,
		sourceEuropePMC:       16,
		sourceOpenAIRE:        20,
		sourceZenodo:          26,
		sourceELife:           21,
		sourcePLOS:            23,
		sourceNBER:            24,
		sourceCrossref:        30,
		sourceCORE:            40,
		sourceDOAJ:            45,
		sourceGutenberg:       5,
		"oapen":               8,
		sourceInternetArchive: 18,
		"doab":                22,
		"hathitrust":          24,
		"googlebooks":         50,
	}
	base, known := bases[source]
	if !known {
		base = 50
	}
	if kind != kindPDF {
		base += 8
	}
	if status == "bronze" {
		base += 6
	}
	if version != "" && version != "publishedVersion" {
		base += 2
	}
	return base
}

func (r *Resolver) client() *http.Client {
	if r != nil && r.Client != nil {
		return r.Client
	}
	return safeHTTPClientTimeout(false, 15*time.Second)
}

func (r *Resolver) contact() string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.ContactEmail)
}

func (r *Resolver) withContact(raw, key string) string {
	if r.contact() == "" {
		return raw
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + key + "=" + url.QueryEscape(r.contact())
}

func (r *Resolver) scholarlyUA() string {
	if email := r.contact(); email != "" {
		return "harvester-mcp/1.0 (mailto:" + email + ")"
	}
	return "harvester-mcp/1.0"
}

func (h *Harvester) resolver() *Resolver {
	if h == nil {
		return &Resolver{}
	}
	return &Resolver{
		Client:                h.oa,
		ContactEmail:          h.settings.contactEmail,
		GoogleBooksAPIKey:     h.settings.googleBooksAPIKey,
		CoreAPIKey:            h.settings.coreAPIKey,
		SemanticScholarAPIKey: h.settings.semanticScholarKey,
		IPFSCatalogURL:        h.settings.ipfsCatalogURL,
		DOIViewerURL:          h.settings.doiViewerURL,
		MD5CatalogURL:         h.settings.md5CatalogURL,
		GoogleScholarURL:      h.settings.googleScholarURL,
	}
}

func (r *Resolver) ResolveDOI(ctx context.Context, doi string) ([]Candidate, error) {
	doi = DOIFrom(doi)
	if doi == "" {
		return nil, fmt.Errorf("invalid DOI")
	}
	ctx = resolverContext(ctx, r)
	client := r.client()
	var specialFailure *doiMetadataFailure
	var out []Candidate
	// Deterministic arXiv DOI routing avoids unnecessary third-party queries.
	const arxivDOIPrefix = "10.48550/arxiv."
	if strings.HasPrefix(strings.ToLower(doi), arxivDOIPrefix) {
		id := doi[len(arxivDOIPrefix):]
		return []Candidate{
			{
				URL:      "https://arxiv.org/pdf/" + id,
				Source:   sourceArXiv,
				Priority: candidatePriority(sourceArXiv, "", "", kindPDF),
				Kind:     kindPDF,
			},
			// ar5iv's HTML rendering (verified live 2026-08-22) is the insurance copy
			// for when the PDF endpoint rate-limits or a wall appears on the CDN.
			{
				URL:      "https://ar5iv.labs.arxiv.org/html/" + id,
				Source:   "ar5iv",
				Priority: candidatePriority(sourceArXiv, "", "", kindHTML),
				Kind:     kindHTML,
			},
		}, nil
	}
	if strings.HasPrefix(strings.ToLower(doi), "10.31235/") || strings.HasPrefix(strings.ToLower(doi), "10.31234/") ||
		strings.HasPrefix(strings.ToLower(doi), "10.31219/") ||
		strings.HasPrefix(strings.ToLower(doi), "10.31730/") ||
		strings.HasPrefix(strings.ToLower(doi), "10.35542/") ||
		strings.HasPrefix(strings.ToLower(doi), "10.33767/") {
		candidates, err := r.osf(ctx, client, doi)
		if err == nil && len(candidates) > 0 {
			return candidates, nil
		}
		if err != nil && !doiMetadataAbsence(err) {
			specialFailure = &doiMetadataFailure{provider: sourceOSF, err: err}
		}
	}
	// Providers are independent and the Python resolver fans them out. Gather
	// concurrently, then apply the explicit priority sort/dedupe so completion
	// order never changes the public candidate order.
	sources := []string{
		sourceUnpaywall,
		sourceOpenAlex,
		sourceSemanticScholar,
		sourceEuropePMC,
		sourceOpenAIRE,
		sourceZenodo,
		sourceELife,
		sourcePLOS,
		sourceNBER,
		sourceCrossref,
		sourceCORE,
		sourceDOAJ,
	}
	results := make([][]Candidate, len(sources))
	errorsBySource := make([]error, len(sources))
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Add(1)
		go func(i int, source string) {
			defer wg.Done()
			defer recoverItem(func(e error) { errorsBySource[i] = e })
			var candidates []Candidate
			var err error
			switch source {
			case sourceUnpaywall:
				candidates, err = r.unpaywall(ctx, client, doi)
			case sourceOpenAlex:
				candidates, err = r.openAlexDOI(ctx, client, doi)
			case sourceSemanticScholar:
				candidates, err = r.semanticScholar(ctx, client, doi)
			case sourceEuropePMC:
				candidates, err = r.europePMCDOI(ctx, client, doi)
			case sourceOpenAIRE:
				candidates, err = r.openAIRE(ctx, client, doi)
			case sourceZenodo:
				candidates, err = r.zenodo(ctx, client, doi)
			case sourceELife:
				candidates, err = r.eLife(ctx, client, doi)
			case sourcePLOS:
				candidates = plosCandidates(doi)
			case sourceNBER:
				candidates = nberCandidates(doi)
			case sourceCrossref:
				candidates, err = r.crossref(ctx, client, doi)
			case sourceCORE:
				candidates, err = r.core(ctx, client, doi)
			case sourceDOAJ:
				candidates, err = r.doaj(ctx, client, doi)
			}
			errorsBySource[i] = err
			results[i] = candidates
		}(i, source)
	}
	wg.Wait()
	for _, candidates := range results {
		out = append(out, candidates...)
	}
	failures := make([]doiMetadataFailure, 0)
	if specialFailure != nil {
		log.Printf("harvest: oa source %s failed for %s: %v", specialFailure.provider, doi, specialFailure.err)
		failures = append(failures, *specialFailure)
	}
	for i, providerErr := range errorsBySource {
		if providerErr == nil || doiMetadataAbsence(providerErr) {
			continue
		}
		// Keep this diagnostic deterministic and internal. Public result
		// rendering maps the typed error to a stable class and never exposes
		// provider URLs or raw transport text.
		log.Printf("harvest: oa source %s failed for %s: %v", sources[i], doi, providerErr)
		failures = append(failures, doiMetadataFailure{provider: sources[i], err: providerErr})
	}
	if len(out) == 0 && len(failures) > 0 {
		kind := errorKindConnect
		for _, failure := range failures {
			if candidateKind := doiMetadataFailureKind(failure.err); candidateKind != "" {
				kind = candidateKind
				break
			}
		}
		return nil, &doiMetadataError{failures: failures, kind: kind}
	}
	return sortCandidates(out), nil
}

func (r *Resolver) ResolveTitle(ctx context.Context, title string) ([]Candidate, error) {
	title = strings.TrimSpace(strings.Trim(title, "\"'"))
	if title == "" {
		return nil, fmt.Errorf("empty title")
	}
	ctx = resolverContext(ctx, r)
	client := r.client()
	doi, titleErr := r.titleToDOI(ctx, client, title)
	if doi != "" {
		candidates, err := r.ResolveDOI(ctx, doi)
		if err == nil && len(candidates) > 0 {
			return candidates, nil
		}
		if err != nil {
			fallback := r.arxivByTitle(ctx, client, title)
			if len(fallback) > 0 {
				return fallback, nil
			}
			return nil, err
		}
	}
	fallback := r.arxivByTitle(ctx, client, title)
	if len(fallback) > 0 {
		return fallback, nil
	}
	if titleErr != nil {
		// Both title-lookup providers were down (F13) and arXiv's own title
		// search also found nothing: report the outage rather than an
		// ordinary empty match — the caller could not otherwise tell "no such
		// title" from "we could not look".
		return nil, titleErr
	}
	return nil, nil
}

// titleToDOI returns the confident title match's DOI, "" for an ordinary
// no-match, and — when BOTH OpenAlex and Crossref failed to answer at all
// (F13) — a non-nil error alongside "" so ResolveTitle can tell that outage
// apart from "no title in either catalogue looked close enough", mirroring
// ResolveDOI's own provider-failure aggregation above.
func (r *Resolver) titleToDOI(ctx context.Context, client *http.Client, title string) (string, error) {
	var data struct {
		Results []struct {
			DOI   string `json:"doi"`
			Name  string `json:"display_name"`
			Title string `json:"title"`
		} `json:"results"`
	}
	openAlexErr := getJSON(
		ctx,
		client,
		r.withContact(
			"https://api.openalex.org/works?filter=title.search:"+url.QueryEscape(title)+"&per_page=5",
			"mailto",
		),
		&data,
	)
	if openAlexErr == nil {
		for _, row := range data.Results {
			name := row.Name
			if name == "" {
				name = row.Title
			}
			if titleSimilarity(title, name) >= .6 {
				if doi := DOIFrom(row.DOI); doi != "" {
					return doi, nil
				}
			}
		}
	}
	var crossref struct {
		Message struct {
			Items []struct {
				DOI   string   `json:"DOI"`
				Title []string `json:"title"`
			} `json:"items"`
		} `json:"message"`
	}
	crossErr := getJSON(
		ctx,
		client,
		r.withContact(
			"https://api.crossref.org/works?query.bibliographic="+url.QueryEscape(title)+"&rows=5&select=DOI,title",
			"mailto",
		),
		&crossref,
	)
	if crossErr == nil {
		for _, item := range crossref.Message.Items {
			name := ""
			if len(item.Title) > 0 {
				name = item.Title[0]
			}
			match := titleSimilarity(title, name)
			if match >= .6 && DOIFrom(item.DOI) != "" {
				return DOIFrom(item.DOI), nil
			}
		}
	}
	var failures []doiMetadataFailure
	if openAlexErr != nil && !doiMetadataAbsence(openAlexErr) {
		failures = append(failures, doiMetadataFailure{provider: sourceOpenAlex, err: openAlexErr})
	}
	if crossErr != nil && !doiMetadataAbsence(crossErr) {
		failures = append(failures, doiMetadataFailure{provider: sourceCrossref, err: crossErr})
	}
	if len(failures) != 2 {
		return "", nil
	}
	kind := errorKindConnect
	for _, failure := range failures {
		if candidateKind := doiMetadataFailureKind(failure.err); candidateKind != "" {
			kind = candidateKind
			break
		}
	}
	return "", &doiMetadataError{subject: "title lookup", failures: failures, kind: kind}
}

func (r *Resolver) arxivByTitle(ctx context.Context, client *http.Client, title string) []Candidate {
	body, status, _, err := getBody(
		ctx,
		client,
		"https://export.arxiv.org/api/query?search_query=ti:%22"+url.QueryEscape(title)+"%22&max_results=3",
		r.scholarlyUA(),
		2*1024*1024,
	)
	if err != nil || status >= 400 {
		return nil
	}
	entries := regexp.MustCompile(`(?is)<entry>(.*?)</entry>`).FindAllSubmatch(body, -1)
	space := regexp.MustCompile(`\s+`)
	for _, entry := range entries {
		part := string(entry[1])
		titleMatch := regexp.MustCompile(`(?is)<title>(.*?)</title>`).FindStringSubmatch(part)
		idMatch := regexp.MustCompile(`(?is)<id>https?://arxiv\.org/abs/([^<]+)</id>`).FindStringSubmatch(part)
		if len(titleMatch) > 1 && len(idMatch) > 1 {
			name := strings.TrimSpace(space.ReplaceAllString(titleMatch[1], " "))
			if titleSimilarity(title, name) >= .6 {
				return []Candidate{
					{
						URL:      "https://arxiv.org/pdf/" + strings.TrimSpace(idMatch[1]),
						Source:   sourceArXiv,
						Priority: 0,
						Kind:     kindPDF,
						Free:     accessGreen,
						Title:    name,
					},
				}
			}
		}
	}
	return nil
}

func (r *Resolver) osf(ctx context.Context, client *http.Client, doi string) ([]Candidate, error) {
	idx := strings.Index(strings.ToLower(doi), "osf.io/")
	if idx < 0 {
		return nil, nil
	}
	guid := doi[idx+len("osf.io/"):]
	guid = strings.Trim(guid, "/")
	if slash := strings.IndexByte(guid, '/'); slash >= 0 {
		guid = guid[:slash]
	}
	if guid == "" {
		return nil, nil
	}
	var data struct {
		Data struct {
			Relationships struct {
				PrimaryFile struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"primary_file"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := getJSONWithHeaders(
		ctx,
		client,
		"https://api.osf.io/v2/preprints/"+url.PathEscape(guid)+"/",
		map[string]string{headerAccept: mediaTypeJSON},
		&data,
	); err != nil {
		return nil, err
	}
	if id := data.Data.Relationships.PrimaryFile.Data.ID; id != "" {
		return []Candidate{
			{URL: "https://osf.io/download/" + url.PathEscape(id) + "/", Source: sourceOSF, Priority: 2, Kind: kindPDF},
		}, nil
	}
	return nil, nil
}

func (r *Resolver) ResolvePMCID(_ context.Context, pmcid string) ([]Candidate, error) {
	pmcid = strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(pmcid)), "pmcid:")))
	if !strings.HasPrefix(pmcid, "PMC") {
		pmcid = "PMC" + pmcid
	}
	return []Candidate{
		{
			URL:      "https://europepmc.org/articles/" + pmcid + "?pdf=render",
			Source:   sourceEuropePMC,
			Priority: 0,
			Kind:     kindPDF,
		},
	}, nil
}

func (r *Resolver) ResolvePMID(ctx context.Context, pmid string) ([]Candidate, error) {
	pmid = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(pmid)), "pmid:"))
	if pmid == "" {
		return nil, fmt.Errorf("invalid PMID")
	}
	ctx = resolverContext(ctx, r)
	var data struct {
		Records []struct {
			PMCID string `json:"pmcid"`
		} `json:"records"`
	}
	if err := getJSON(
		ctx,
		r.client(),
		r.withContact(
			"https://pmc.ncbi.nlm.nih.gov/tools/idconv/api/v1/articles/?ids="+url.QueryEscape(
				pmid,
			)+"&format=json&tool=harvester-mcp",
			"email",
		),
		&data,
	); err != nil {
		return nil, err
	}
	if len(data.Records) == 0 || data.Records[0].PMCID == "" {
		return nil, fmt.Errorf("PubMed ID %s has no open-access PMCID", pmid)
	}
	return r.ResolvePMCID(ctx, data.Records[0].PMCID)
}

func getJSON(ctx context.Context, client *http.Client, raw string, dst any) error {
	return getJSONWithHeaders(ctx, client, raw, nil, dst)
}

// resolverJSONMaxBody bounds a scholarly JSON API response. postJSON and
// getJSONWithHeaders both set gatewayRequest.oversizeTruncate to false at
// this same ceiling, so an over-ceiling body is refused with an error naming
// the ceiling on either path, never silently truncated into an
// "unexpected end of JSON input" decode failure.
const resolverJSONMaxBody = 10 * 1024 * 1024

func postJSON(ctx context.Context, client *http.Client, raw string, payload, dst any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	// Through the fetch gateway, like every other harvester egress. Escalation
	// is OFF: this is a JSON API, and a browser render can never satisfy one.
	// The gateway also imposes the byte ceiling this call previously lacked —
	// it decoded straight off the socket with no bound at all.
	response, err := gatewayAttempt(ctx, gatewayRequest{
		url:    raw,
		method: http.MethodPost,
		body:   body,
		client: client,
		ua:     contextualUA(ctx),
		headers: http.Header{
			headerAccept:   {mediaTypeJSON},
			"Content-Type": {mediaTypeJSON},
		},
		max:              resolverJSONMaxBody,
		policy:           gatewayNoEscalate,
		oversizeTruncate: false,
	})
	if err != nil {
		return err
	}
	if response.status >= 400 {
		return fmt.Errorf("HTTP %d", response.status)
	}
	if err := json.Unmarshal(response.body, dst); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

// contextualUA is the resolver's polite scholarly identity when the call
// runs under resolverContext, else the bare harvester UA.
func contextualUA(ctx context.Context) string {
	if contextual, ok := ctx.Value(resolverUAContextKey{}).(string); ok && contextual != "" {
		return contextual
	}
	return searchUA
}

// getJSONBody is the ONE JSON-decoding read every caller in this package
// shares: it goes through the gateway with oversizeTruncate OFF, so a body
// over the ceiling is refused with an error naming the ceiling, never
// truncated. Truncating a JSON body and then handing json.Unmarshal a cut-off
// prefix produces "unexpected end of JSON input", which reads as a malformed
// response from the source when the real story is that the ceiling was hit.
// getJSONWithHeaders below is the contextual-UA convenience wrapper every
// scholarly resolver call uses; a caller that needs an explicit UA or a
// different byte ceiling (the legacy book/mirror lookups, each pinned to its
// own historical max) calls this directly instead of duplicating the
// gateway/decode plumbing.
func getJSONBody(
	ctx context.Context,
	client *http.Client,
	raw, ua string,
	headers map[string]string,
	maxBytes int64,
	dst any,
) error {
	header := make(http.Header, len(headers))
	for key, value := range headers {
		header.Set(key, value)
	}
	response, err := gatewayAttempt(ctx, gatewayRequest{
		url:              raw,
		client:           client,
		ua:               ua,
		headers:          header,
		max:              maxBytes,
		oversizeTruncate: false,
	})
	if err != nil {
		return err
	}
	if response.status >= 400 {
		return fmt.Errorf("HTTP %d", response.status)
	}
	if err := json.Unmarshal(response.body, dst); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

func getJSONWithHeaders(
	ctx context.Context,
	client *http.Client,
	raw string,
	headers map[string]string,
	dst any,
) error {
	return getJSONBody(ctx, client, raw, contextualUA(ctx), headers, resolverJSONMaxBody, dst)
}

func sortCandidates(in []Candidate) []Candidate {
	sort.SliceStable(in, func(i, j int) bool { return in[i].Priority < in[j].Priority })
	seen := map[string]bool{}
	out := []Candidate{}
	for _, c := range in {
		if c.URL != "" && !seen[c.URL] {
			seen[c.URL] = true
			out = append(out, c)
		}
	}
	return out
}

func titleSimilarity(a, b string) float64 {
	aa := titleTokens(a)
	bb := titleTokens(b)
	if len(aa) == 0 || len(bb) == 0 {
		return 0
	}
	intersect := 0
	for x := range aa {
		if bb[x] {
			intersect++
		}
	}
	return float64(intersect) / float64(len(aa)+len(bb)-intersect)
}

var titleTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

func titleTokens(value string) map[string]bool {
	out := map[string]bool{}
	for _, token := range titleTokenPattern.FindAllString(strings.ToLower(value), -1) {
		out[token] = true
	}
	return out
}

func titleMatch(query, title string) float64 {
	j := titleSimilarity(query, title)
	q, t := titleTokens(query), titleTokens(title)
	if len(q) > 0 && len(q) <= len(t) {
		contained := true
		for token := range q {
			if !t[token] {
				contained = false
				break
			}
		}
		if contained && j < .85 {
			return .85
		}
	}
	return j
}
