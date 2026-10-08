// Package modelcost refreshes the price table from the two official pricing
// pages: it fetches and parses both pages, derives the table's rows from their
// standard text-token tables, and persists them to the clone's prices.json at
// most once a day. A fetch or parse failure is an error, never a stale or zero
// price.
package modelcost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/harvest"
)

const (
	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"
	maxPageBytes      = 1 << 20
	openAIURL         = "https://developers.openai.com/api/docs/pricing.md"
	claudeURL         = "https://platform.claude.com/docs/en/about-claude/pricing.md"
)

// Cell is one table cell as the publisher wrote it.
type Cell struct {
	Text string
}

// Table is a lossless table with its original headers and source lines.
type Table struct {
	Heading string
	Context string
	Line    int
	Headers []string
	Rows    [][]Cell
}

// Source is the exact successfully retrieved publisher document.
type Source struct {
	URL       string
	FetchedAt string
	Markdown  string
}

// Model holds every row that explicitly names this published model.
type Model struct {
	ID     string
	Tables []Table
}

// Catalog holds all models and ALL tables from one provider's page.
// PageContext retains every non-table paragraph, including page-wide modifiers,
// footnotes and examples that must not be mistaken for a particular model rate.
type Catalog struct {
	Provider    string
	Currency    string
	Models      []Model
	Tables      []Table
	PageContext string
	Source      Source
}

// Fetcher retrieves and parses both official pricing pages.
type Fetcher struct {
	client *http.Client
	clock  clock.Clock
}

// NewFetcher returns a Fetcher; a nil client uses harvest's public-address client.
func NewFetcher(client *http.Client, ticker clock.Clock) *Fetcher {
	if client == nil {
		client = harvest.NewDirectClient(45*time.Second, nil, "Professor model-cost/1.0", nil)
	}
	if ticker == nil {
		ticker = clock.Real
	}
	return &Fetcher{client: client, clock: ticker}
}

// Fetch retrieves and parses both official pricing pages, Claude first; any
// fetch or parse failure is an error, never a partial catalog.
func (s *Fetcher) Fetch(ctx context.Context) ([]Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	catalogs := make([]Catalog, 0, 2)
	for _, provider := range []struct{ name, url string }{{providerAnthropic, claudeURL}, {providerOpenAI, openAIURL}} {
		source, err := s.fetchSource(ctx, provider.url)
		if err != nil {
			return nil, err
		}
		catalog, err := parseCatalog(provider.name, source)
		if err != nil {
			return nil, fmt.Errorf("model-cost: parse %s: %w", provider.url, err)
		}
		catalogs = append(catalogs, catalog)
	}
	return catalogs, nil
}

func (s *Fetcher) fetchSource(ctx context.Context, sourceURL string) (Source, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, http.NoBody)
	if err != nil {
		return Source{}, fmt.Errorf("model-cost: request %s: %w", sourceURL, err)
	}
	request.Header.Set("Accept", "text/markdown, text/plain")
	request.Header.Set("Cache-Control", "no-cache, no-store")
	// Even publisher redirects stay on the two fixed official hosts. Harvest
	// additionally validates/pins public DNS addresses and rejects private IPs.
	client := *s.client
	originalRedirect := client.CheckRedirect
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || (r.URL.Host != "developers.openai.com" && r.URL.Host != "platform.claude.com") {
			return errors.New("model-cost: publisher redirect left the official HTTPS hosts")
		}
		if len(via) >= 5 {
			return errors.New("model-cost: too many publisher redirects")
		}
		if originalRedirect != nil {
			return originalRedirect(r, via)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return Source{}, fmt.Errorf("model-cost: fetch %s: %w", sourceURL, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Source{}, fmt.Errorf("model-cost: fetch %s: HTTP %d", sourceURL, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPageBytes+1))
	if err != nil {
		return Source{}, fmt.Errorf("model-cost: read %s: %w", sourceURL, err)
	}
	if len(body) > maxPageBytes {
		return Source{}, fmt.Errorf("model-cost: %s exceeds %d bytes", sourceURL, maxPageBytes)
	}
	text := string(body)
	if strings.TrimSpace(text) == "" {
		return Source{}, fmt.Errorf("model-cost: %s returned an empty source", sourceURL)
	}
	if strings.Contains(strings.ToLower(text), "<html") || strings.Contains(strings.ToLower(text), "<!doctype") {
		return Source{}, fmt.Errorf("model-cost: %s did not return markdown pricing", sourceURL)
	}
	return Source{
		URL:       sourceURL,
		FetchedAt: s.clock.Now().UTC().Format(time.RFC3339Nano),
		Markdown:  text,
	}, nil
}

var (
	linkPattern       = regexp.MustCompile(`\[([^]]+)\]\([^)]*\)`)
	tagPattern        = regexp.MustCompile(`<[^>]*>`)
	claudeNamePattern = regexp.MustCompile(`(?i)claude\s+[a-z]+(?:\s+\d+(?:\.\d+)?)?`)
)

func parseCatalog(provider string, source Source) (Catalog, error) {
	catalog := Catalog{
		Provider: provider,
		Currency: "USD",
		Source:   source,
		Tables:   []Table{},
		Models:   []Model{},
	}
	lines := strings.Split(source.Markdown, "\n")
	var contextLines, localContext []string
	heading := ""
	models := map[string][]Table{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			heading = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		}
		if i+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(line), "|") || !isSeparator(lines[i+1]) {
			if strings.HasPrefix(strings.TrimSpace(line), "|") {
				return Catalog{}, fmt.Errorf("line %d: unsupported table layout; catalog incomplete", i+1)
			}
			contextLines = append(contextLines, line)
			localContext = append(localContext, line)
			continue
		}
		table := Table{
			Heading: heading,
			Context: strings.Join(localContext, "\n"),
			Line:    i + 1,
			Headers: splitCells(line),
			Rows:    [][]Cell{},
		}
		column := modelColumn(table.Headers)
		if column < 0 {
			for _, header := range table.Headers {
				if strings.Contains(strings.ToLower(header), "model") {
					return Catalog{}, fmt.Errorf(
						"line %d: unsupported model column %q; catalog incomplete",
						i+1,
						header,
					)
				}
			}
		}
		i += 2
		for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
			cells := splitCells(lines[i])
			if len(cells) != len(table.Headers) {
				return Catalog{}, fmt.Errorf(
					"line %d: column count %d differs from header %d; catalog incomplete",
					i+1,
					len(cells),
					len(table.Headers),
				)
			}
			row := make([]Cell, len(cells))
			for j, cell := range cells {
				row[j] = Cell{Text: cell}
			}
			table.Rows = append(table.Rows, row)
			if column >= 0 {
				ids := publishedIDs(provider, cells[column])
				if len(ids) == 0 {
					return Catalog{}, fmt.Errorf(
						"line %d: unsupported model label %q; catalog incomplete",
						i+1,
						cells[column],
					)
				}
				for _, id := range ids {
					entry := table
					entry.Rows = [][]Cell{row}
					models[id] = append(models[id], entry)
				}
			}
		}
		if len(table.Rows) == 0 {
			return Catalog{}, fmt.Errorf("line %d: pricing table has no rows", table.Line)
		}
		catalog.Tables = append(catalog.Tables, table)
		localContext = nil
		i--
	}
	if len(catalog.Tables) == 0 || len(models) == 0 {
		return Catalog{}, errors.New("no model pricing tables found; publisher layout changed")
	}
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		catalog.Models = append(catalog.Models, Model{ID: id, Tables: models[id]})
	}
	catalog.PageContext = strings.Join(contextLines, "\n")
	return catalog, nil
}

func publishedIDs(provider, label string) []string {
	label = tagPattern.ReplaceAllString(linkPattern.ReplaceAllString(label, "$1"), "")
	if provider == providerAnthropic {
		var ids []string
		for _, name := range claudeNamePattern.FindAllString(label, -1) {
			ids = append(ids, strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(name), " ", "-"), ".", "-"))
		}
		return ids
	}
	label, _, _ = strings.Cut(label, " (")
	label = strings.Trim(strings.TrimSpace(label), "`*")
	if label == "" || strings.ContainsAny(label, " /") {
		return nil
	}
	// Whisper is the pricing page's display name; the official model page names
	// its sole API ID whisper-1. This is a publisher label mapping, not a
	// guessed snapshot or a match against another model's price.
	if strings.EqualFold(label, "Whisper") {
		return []string{"whisper-1"}
	}
	return []string{strings.ToLower(label)}
}

func modelColumn(headers []string) int {
	for i, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header), "Model") {
			return i
		}
	}
	return -1
}

func isSeparator(line string) bool {
	if !strings.HasPrefix(strings.TrimSpace(line), "|") {
		return false
	}
	for _, cell := range splitCells(line) {
		if strings.TrimSpace(cell) == "" || !strings.Contains(cell, "-") || strings.Trim(cell, "-: ") != "" {
			return false
		}
	}
	return true
}

func splitCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	var cells []string
	start, escaped := 0, false
	for i, r := range line {
		if r == '|' && !escaped {
			cells = append(cells, strings.TrimSpace(line[start:i]))
			start = i + 1
		}
		if r == '\\' {
			escaped = !escaped
		} else {
			escaped = false
		}
	}
	return append(cells, strings.TrimSpace(line[start:]))
}
