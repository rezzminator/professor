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
	"time"
)

// FindWorks is FindWorksReport's ranked candidates. With no candidate and a
// failed source it is an error naming each failed source: an outage is never
// an empty answer.
func (r *Resolver) FindWorks(ctx context.Context, query string, limit int) ([]Candidate, error) {
	found, err := r.FindWorksReport(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	if failed := found.Failed(); len(found.Candidates) == 0 && len(failed) > 0 {
		return nil, fmt.Errorf("discovery sources failed: %s", FailedText(failed))
	}
	return found.Candidates, nil
}

// FindWorksReport is FindWorksReportFor any kind of work.
func (r *Resolver) FindWorksReport(ctx context.Context, query string, limit int) (WorksFound, error) {
	return r.FindWorksReportFor(ctx, query, limit, "")
}

// FindWorksReportFor searches every discovery source at once and names each
// source's status beside the ranked candidates; it is an error only when no
// source answered. kind ("paper", "book", or "" / "any") picks the core
// sources the call waits for before its short grace for the rest.
func (r *Resolver) FindWorksReportFor(ctx context.Context, query string, limit int, kind string) (WorksFound, error) {
	return r.searchLiteratureWithin(ctx, query, limit, kind, searchLiteratureTimeout, searchLiteratureGrace)
}

// searchLiteratureTimeout is the whole search_literature call's deadline: an MCP client gives a
// tool call about 60 s, and the sources that run long are the ones that fail.
const searchLiteratureTimeout = 20 * time.Second

// searchLiteratureGrace is how long a search_literature call waits for the other sources once
// the core sources for the asked-for kind have finished and the ranking is
// settled: a source that runs long past the core is the one that fails.
const searchLiteratureGrace = 2 * time.Second

// searchLiteratureNotWaitedText names a source still running when the grace ran out,
// apart from the deadline's text.
const searchLiteratureNotWaitedText = "not waited for: the core sources had answered"

// searchLiteratureWithin is FindWorksReportFor under a deadline: every source runs at
// once, and when the deadline comes the call ranks what answered; a source
// still running is cancelled and named timed_out with the time it was given.
// Once the core sources for kind finished (searchLiteratureCore), the call waits
// grace for the rest, then ranks what answered.
func (r *Resolver) searchLiteratureWithin(
	ctx context.Context,
	query string,
	limit int,
	kind string,
	deadline, grace time.Duration,
) (WorksFound, error) {
	query = strings.TrimSpace(strings.Trim(query, "\"'"))
	if query == "" {
		return WorksFound{Candidates: []Candidate{}, Sources: []WorkSource{}}, nil
	}
	if limit <= 0 {
		limit = 8
	}
	ctx, cancel := context.WithTimeout(resolverContext(ctx, r), deadline)
	defer cancel()
	client := r.client()
	searches := []workSearch{}
	for _, gatherer := range []struct {
		name   string
		hosts  int
		gather func(context.Context, *http.Client, string, int) []Candidate
	}{
		{nameOpenAlex, 1, r.findPapers},
		{nameArXiv, 1, r.findArxiv},
		{nameCrossref, 1, r.findCrossref},
		{nameSemanticScholar, 1, r.findSemanticScholar},
		{nameOpenLibrary, 1, r.findOpenLibrary},
		{nameGutendex, 1, r.findGutendex},
	} {
		searches = append(
			searches,
			workSearch{
				gatherer.name,
				searchLiteratureCore(kind, gatherer.name),
				func(ctx context.Context) ([]Candidate, string) {
					probe, probed := newSourceProbe(client)
					candidates := gatherer.gather(ctx, probed, query, limit)
					return candidates, probe.failure(gatherer.hosts)
				},
			},
		)
	}
	for _, optional := range []struct {
		name, label string
		enabled     bool
		search      func(context.Context, string, int) ([]Candidate, error)
	}{
		{nameBookMirror, "ipfs-catalog", r.configuredProviderBase(sourceIPFSCatalog) != "", r.ipfsCatalogSearch},
		{nameBookMirror, "md5-catalog", r.configuredProviderBase(sourceMD5Catalog) != "", r.md5CatalogSearch},
		{"Google Scholar", "google scholar", strings.TrimSpace(r.GoogleScholarURL) != "", r.googleScholar},
	} {
		if !optional.enabled {
			continue
		}
		searches = append(
			searches,
			workSearch{
				optional.name,
				searchLiteratureCore(kind, optional.name),
				func(ctx context.Context) ([]Candidate, string) {
					candidates, err := optional.search(ctx, query, limit)
					if err != nil {
						log.Printf("harvest: %s discovery failed for %s: %v", optional.label, query, err)
						return nil, redactFailureText(err.Error())
					}
					return candidates, ""
				},
			},
		)
	}
	// An "any" call finishes early only with limit candidates in hand.
	minCandidates := 0
	if kind != kindPaper && kind != kindBook {
		minCandidates = limit
	}
	parts, sources := gatherWorks(ctx, searches, deadline, grace, minCandidates)
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
	// Equal title matches rank an open record first, then the earliest dated
	// one: a re-registration, a reprint or a repository copy of a work carries
	// the original's title under a later date, and listing order is provider
	// order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Match != out[j].Match {
			return out[i].Match > out[j].Match
		}
		if fi, fj := free[strings.ToLower(out[i].Free)], free[strings.ToLower(out[j].Free)]; fi != fj {
			return fi
		}
		if yi, yj := out[i].Year, out[j].Year; yi != yj {
			return yj == 0 || (yi != 0 && yi < yj) // an undated record after the dated ones
		}
		return false
	})
	if len(out) > limit {
		out = out[:limit]
	}
	found := WorksFound{Candidates: out, Sources: sources}
	if len(out) == 0 && len(found.Failed()) == len(sources) {
		return found, errNoSourceAnswered(found)
	}
	return found, nil
}

// The discovery sources' public names, as a search_literature answer names them.
const (
	nameOpenAlex        = "OpenAlex"
	nameArXiv           = "arXiv"
	nameCrossref        = "Crossref"
	nameSemanticScholar = "Semantic Scholar"
	nameOpenLibrary     = "Open Library"
	nameGutendex        = "Gutendex"
	nameBookMirror      = "book mirror"
)

// searchLiteratureCore reports whether a search_literature call for kind waits for source
// before its grace: a paper call waits for the paper sources, a book call for
// the book sources, any other call for the paper sources and Open Library.
func searchLiteratureCore(kind, source string) bool {
	paper := source == nameOpenAlex || source == nameArXiv || source == nameCrossref || source == nameSemanticScholar
	switch kind {
	case kindPaper:
		return paper
	case kindBook:
		return source == nameOpenLibrary || source == nameGutendex || source == nameBookMirror
	}
	return paper || source == nameOpenLibrary
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
		// FindWorksReport's source probe saw the failed request and names
		// OpenAlex as failed; nil here is not read as an empty answer.
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

// findArxiv ranks an arXiv hit by its own title and dates it by its
// identifier (YYMM.NNNNN): a flat discount and the query as its title ranked
// the original below any later record carrying the same title.
func (r *Resolver) findArxiv(ctx context.Context, client *http.Client, query string, _ int) []Candidate {
	candidates := r.arxivByTitle(ctx, client, query)
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Kind = kindPaper
		if candidate.Title == "" {
			candidate.Title = query
		}
		candidate.Free = accessGreen
		candidate.Match = roundMatch(titleMatch(query, candidate.Title))
		if id := arxivNewStyleID.FindStringSubmatch(candidate.URL); id != nil {
			candidate.Year = 2000 + int(id[1][0]-'0')*10 + int(id[1][1]-'0')
		}
		out = append(out, candidate)
	}
	return out
}

var arxivNewStyleID = regexp.MustCompile(`/(\d{2})(0[1-9]|1[012])\.\d{4,5}(v\d+)?$`)

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
	// select asks for only the fields ranked below: a whole work record per row
	// is Crossref's slow path, and a slow answer is a timed-out one.
	endpoint := r.withContact("https://api.crossref.org/works?query.bibliographic="+url.QueryEscape(query)+
		fmt.Sprintf("&rows=%d&select=DOI,title,issued", limit), "mailto")
	out := []Candidate{}
	if err := getJSON(ctx, client, endpoint, &data); err != nil {
		// An outage is not an empty shelf — name it instead of returning a
		// silent nil that reads as "Crossref knows nothing about this title".
		log.Printf("harvest: search_literature crossref search failed for %q: %v", query, err)
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
		log.Printf("harvest: search_literature semantic-scholar search failed for %q: %v", query, err)
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
