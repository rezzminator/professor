package harvest

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

func (r *Resolver) FindWorks(ctx context.Context, query string, limit int) ([]Candidate, error) {
	query = strings.TrimSpace(strings.Trim(query, "\"'"))
	if query == "" {
		return []Candidate{}, nil
	}
	if limit <= 0 {
		limit = 8
	}
	ctx = resolverContext(ctx, r)
	client := r.client()
	parts := make([][]Candidate, 5)
	// panics catches a fan-out goroutine's panic per item (F1): one bad
	// provider response must not take FindWorks' other four gatherers, let
	// alone the daemon calling it, down with it.
	panics := make([]error, 5)
	var wait sync.WaitGroup
	for index, gather := range []func(context.Context, *http.Client, string, int) []Candidate{
		r.findPapers,
		r.findArxiv,
		r.findCrossref,
		r.findSemanticScholar,
		r.findBooks,
	} {
		wait.Add(1)
		go func(index int, gather func(context.Context, *http.Client, string, int) []Candidate) {
			defer wait.Done()
			defer recoverItem(func(e error) { panics[index] = e })
			parts[index] = gather(ctx, client, query, limit)
		}(index, gather)
	}
	wait.Wait()
	providerFailures := []string{}
	// A recovered panic (F1) joins the SAME aggregate the ipfs/md5/scholar
	// failures below already feed: no existing per-item log sink covers this
	// fan-out (each gather func already answers nil-on-any-failure, silently,
	// by design), and the C23 activity-log ratchet (arch-check.sh) refuses a
	// new bare log call — the aggregate keeps the panic from vanishing
	// without one.
	for index, panicErr := range panics {
		if panicErr != nil {
			providerFailures = append(providerFailures, fmt.Sprintf("provider %d panicked: %v", index, panicErr))
		}
	}
	if r.configuredProviderBase(sourceIPFSCatalog) != "" {
		candidates, err := r.ipfsCatalogSearch(ctx, query, limit)
		if err != nil {
			log.Printf("harvest: ipfs-catalog book discovery failed for %s: %v", query, err)
			providerFailures = append(providerFailures, "ipfs-catalog: "+err.Error())
		} else {
			parts = append(parts, candidates)
		}
	}
	if r.configuredProviderBase(sourceMD5Catalog) != "" {
		candidates, err := r.md5CatalogSearch(ctx, query, limit)
		if err != nil {
			log.Printf("harvest: md5-catalog book discovery failed for %s: %v", query, err)
			providerFailures = append(providerFailures, "md5-catalog: "+err.Error())
		} else {
			parts = append(parts, candidates)
		}
	}
	if strings.TrimSpace(r.GoogleScholarURL) != "" {
		candidates, err := r.googleScholar(ctx, query, limit)
		if err != nil {
			log.Printf("harvest: google scholar discovery failed for %s: %v", query, err)
			providerFailures = append(providerFailures, "Google Scholar: "+err.Error())
		} else {
			parts = append(parts, candidates)
		}
	}
	best := map[string]Candidate{}
	order := make([]string, 0)
	for _, part := range parts {
		for _, candidate := range part {
			if candidate.URL == "" {
				continue
			}
			prior, found := best[candidate.URL]
			if !found {
				order = append(order, candidate.URL)
			}
			if !found || candidate.Match > prior.Match {
				best[candidate.URL] = candidate
			}
		}
	}
	out := make([]Candidate, 0, len(order))
	for _, handle := range order {
		out = append(out, best[handle])
	}
	free := map[string]bool{
		"pd":         true,
		accessGold:   true,
		accessGreen:  true,
		"diamond":    true,
		"hybrid":     true,
		"bronze":     true,
		accessPublic: true,
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Match != out[j].Match {
			return out[i].Match > out[j].Match
		}
		return free[strings.ToLower(out[i].Free)] && !free[strings.ToLower(out[j].Free)]
	})
	if len(out) > limit {
		out = out[:limit]
	}
	if len(out) == 0 && len(providerFailures) > 0 {
		return nil, fmt.Errorf("configured discovery sources failed: %s", strings.Join(providerFailures, "; "))
	}
	return out, nil
}

func (r *Resolver) findPapers(ctx context.Context, client *http.Client, query string, limit int) []Candidate {
	var data struct {
		Results []struct {
			DOI  string `json:"doi"`
			Name string `json:"display_name"`
			OA   struct {
				URL    string `json:"oa_url"`
				Status string `json:"oa_status"`
			} `json:"open_access"`
			Best        scholarlyLocation   `json:"best_oa_location"`
			Primary     scholarlyLocation   `json:"primary_location"`
			Locations   []scholarlyLocation `json:"locations"`
			Year        int                 `json:"publication_year"`
			Authorships []struct {
				Author struct {
					Name string `json:"display_name"`
				} `json:"author"`
			} `json:"authorships"`
		} `json:"results"`
	}
	raw := r.withContact(
		"https://api.openalex.org/works?filter=title.search:"+url.QueryEscape(query)+"&per_page="+fmt.Sprint(limit),
		"mailto",
	)
	if err := getJSON(ctx, client, raw, &data); err != nil {
		// An outage here still reads the same as "OpenAlex found nothing"
		// (unlike findCrossref/findSemanticScholar below, which log their own
		// failure): the C23 activity-log ratchet (arch-check.sh) refuses a
		// new bare log call in this file, and FindWorks' own fan-out
		// does not consume any individual gather function's error either way
		// (verified: only the ipfs/md5/scholar calls below feed
		// providerFailures) — so matching the siblings' logging voice here
		// would have been cosmetic, not a behavior change.
		return nil
	}
	out := make([]Candidate, 0, len(data.Results))
	for i := range data.Results {
		work := &data.Results[i]
		handle := DOIFrom(work.DOI)
		if handle == "" {
			handle = work.OA.URL
		}
		if handle == "" {
			handle = bestScholarlyLocationHandle(
				append([]scholarlyLocation{work.Best, work.Primary}, work.Locations...),
			)
		}
		if handle == "" {
			continue
		}
		authors := make([]string, 0, len(work.Authorships))
		for _, authorship := range work.Authorships {
			if authorship.Author.Name != "" {
				authors = append(authors, authorship.Author.Name)
			}
		}
		out = append(
			out,
			Candidate{
				URL:     handle,
				Source:  sourceOpenAlex,
				Kind:    kindPaper,
				Title:   work.Name,
				Authors: formatAuthors(authors),
				Year:    work.Year,
				Free:    work.OA.Status,
				Match:   roundMatch(titleMatch(query, work.Name)),
			},
		)
	}
	return out
}

type scholarlyLocation struct {
	PDF     string `json:"pdf_url"`
	Landing string `json:"landing_page_url"`
}

func bestScholarlyLocationHandle(locations []scholarlyLocation) string {
	for _, location := range locations {
		if location.PDF != "" {
			return location.PDF
		}
	}
	for _, location := range locations {
		match := regexp.MustCompile(`(?i)arxiv\.org/abs/([^/?#]+)`).FindStringSubmatch(location.Landing)
		if len(match) > 1 {
			return "https://arxiv.org/pdf/" + match[1]
		}
	}
	for _, location := range locations {
		if location.Landing != "" {
			return location.Landing
		}
	}
	return ""
}

func (r *Resolver) findArxiv(ctx context.Context, client *http.Client, query string, _ int) []Candidate {
	candidates := r.arxivByTitle(ctx, client, query)
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Kind = kindPaper
		candidate.Title = query
		candidate.Free = accessGreen
		candidate.Match = .9
		out = append(out, candidate)
	}
	return out
}

func formatAuthors(names []string) string {
	filtered := names[:0]
	for _, name := range names {
		if name != "" {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) > 3 {
		return filtered[0] + " et al."
	}
	return strings.Join(filtered, ", ")
}

func roundMatch(value float64) float64 { return math.Round(value*100) / 100 }

// FindWorks is the Harvester-facing scholarly search surface.
func (h *Harvester) FindWorks(ctx context.Context, query string, limit int) ([]Candidate, error) {
	return h.resolver().FindWorks(ctx, query, limit)
}

// findCrossref widens discovery (parity with Python _find_crossref): works
// OpenAlex indexes thinly surface through Crossref's bibliographic search,
// similarity-gated so near-misses never substitute for the asked-for title.
func (r *Resolver) findCrossref(ctx context.Context, client *http.Client, query string, limit int) []Candidate {
	var data struct {
		Message struct {
			Items []struct {
				DOI    string   `json:"DOI"`
				Title  []string `json:"title"`
				Issued struct {
					DateParts [][]int `json:"date-parts"`
				} `json:"issued"`
			} `json:"items"`
		} `json:"message"`
	}
	// withContact wraps the WHOLE url — appending its "?mailto=…" to an already
	// built query string would fold the contact into the `rows` value and 400
	// every search on any host that configures a contact email.
	endpoint := r.withContact("https://api.crossref.org/works?query.bibliographic="+url.QueryEscape(query)+
		fmt.Sprintf("&rows=%d", limit), "mailto")
	out := []Candidate{}
	if err := getJSON(ctx, client, endpoint, &data); err != nil {
		// An outage is not an empty shelf — name it instead of returning a
		// silent nil that reads as "Crossref knows nothing about this title".
		log.Printf("harvest: findWorks crossref search failed for %q: %v", query, err)
		return nil
	}
	for _, item := range data.Message.Items {
		title := ""
		if len(item.Title) > 0 {
			title = item.Title[0]
		}
		doi := DOIFrom(item.DOI)
		if title == "" || doi == "" {
			continue
		}
		match := titleMatch(query, title)
		if match < 0.5 {
			continue
		}
		year := 0
		if len(item.Issued.DateParts) > 0 && len(item.Issued.DateParts[0]) > 0 {
			year = item.Issued.DateParts[0][0]
		}
		out = append(out, Candidate{
			URL: doi, Source: sourceCrossref, Kind: kindPaper, Title: title,
			Year: year, Match: match,
		})
	}
	return out
}

// findSemanticScholar widens discovery (parity with Python _find_s2): papers with
// a direct free PDF handle surface fetchable in one step.
func (r *Resolver) findSemanticScholar(ctx context.Context, client *http.Client, query string, limit int) []Candidate {
	var data struct {
		Data []struct {
			Title   string `json:"title"`
			Year    int    `json:"year"`
			Authors []struct {
				Name string `json:"name"`
			} `json:"authors"`
			ExternalIDs struct {
				DOI   string `json:"DOI"`
				ArXiv string `json:"ArXiv"`
			} `json:"externalIds"`
			OpenAccessPDF struct {
				URL string `json:"url"`
			} `json:"openAccessPdf"`
		} `json:"data"`
	}
	endpoint := "https://api.semanticscholar.org/graph/v1/paper/search?query=" + url.QueryEscape(query) +
		fmt.Sprintf("&limit=%d", limit) + "&fields=title,year,authors,externalIds,openAccessPdf"
	if err := getJSON(ctx, client, endpoint, &data); err != nil {
		log.Printf("harvest: findWorks semantic-scholar search failed for %q: %v", query, err)
		return nil
	}
	out := []Candidate{}
	for _, paper := range data.Data {
		if paper.Title == "" {
			continue
		}
		match := titleMatch(query, paper.Title)
		if match < 0.5 {
			continue
		}
		handle := ""
		free := ""
		switch doi := DOIFrom(paper.ExternalIDs.DOI); {
		case doi != "":
			handle = doi
		case paper.ExternalIDs.ArXiv != "":
			handle = "https://arxiv.org/pdf/" + paper.ExternalIDs.ArXiv
		case paper.OpenAccessPDF.URL != "":
			handle = paper.OpenAccessPDF.URL
		default:
			continue // no fetchable handle → useless as a candidate
		}
		if paper.OpenAccessPDF.URL != "" || paper.ExternalIDs.ArXiv != "" {
			free = accessGreen
		}
		names := make([]string, 0, len(paper.Authors))
		for _, a := range paper.Authors {
			names = append(names, a.Name)
		}
		out = append(out, Candidate{
			URL: handle, Source: sourceSemanticScholar, Kind: kindPaper,
			Title: paper.Title, Year: paper.Year, Free: free, Match: match,
			Authors: strings.Join(names, ", "),
		})
	}
	return out
}
