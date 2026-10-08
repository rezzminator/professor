// Package modelcost reads the current Claude and OpenAI API prices from the
// two official pricing pages for `pfm model-cost`. Every lookup fetches both
// pages; a fetch or parse failure is an error, never a stale or zero price.
package modelcost

import (
	"context"
	"crypto/sha256"
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

// Input selects a published model or both complete provider catalogs.
type Input struct {
	Model string `json:"model,omitempty"`
	All   bool   `json:"all,omitempty"`
}

// Cell preserves the publisher's text, including every exact numeric
// spelling. Numbers are source tokens, not converted amounts: headers and
// context establish their currencies, units, thresholds and conditions.
type Cell struct {
	Text    string   `json:"text"`
	Numbers []string `json:"numbers"`
}

// Table is a lossless table with its original headers and source lines.
type Table struct {
	Heading string   `json:"heading"`
	Context string   `json:"context"`
	Line    int      `json:"line"`
	Headers []string `json:"headers"`
	Rows    [][]Cell `json:"rows"`
}

// Source is the exact successfully retrieved publisher document.
type Source struct {
	URL       string `json:"url"`
	FetchedAt string `json:"fetched_at"`
	SHA256    string `json:"sha256"`
	Markdown  string `json:"markdown"`
}

// Model holds every row that explicitly names this published model.
type Model struct {
	ID     string  `json:"id"`
	Tables []Table `json:"tables"`
}

// Catalog holds all models and ALL tables from one provider's page.
// PageContext retains every non-table paragraph, including page-wide modifiers,
// footnotes and examples that must not be mistaken for a particular model rate.
type Catalog struct {
	Provider       string   `json:"provider"`
	Currency       string   `json:"currency"`
	Models         []Model  `json:"models"`
	Tables         []Table  `json:"tables"`
	PageContext    string   `json:"page_context"`
	ContextNumbers []string `json:"context_numbers"`
	Source         Source   `json:"source"`
}

// Output is a lookup or both complete catalogs, with cache freshness.
type Output struct {
	Model          string    `json:"model,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	Currency       string    `json:"currency,omitempty"`
	Tables         []Table   `json:"tables,omitempty"`
	Source         *Source   `json:"source,omitempty"`
	PageContext    string    `json:"page_context,omitempty"`
	ContextNumbers []string  `json:"context_numbers,omitempty"`
	Catalogs       []Catalog `json:"catalogs,omitempty"`
	Coverage       string    `json:"coverage"`
}

// Fetcher retrieves and parses both official pricing pages on every lookup.
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

// Lookup returns one published model's rows, or both complete catalogs.
func (s *Fetcher) Lookup(ctx context.Context, input Input) (Output, error) {
	model, provider, err := Identity(input)
	if err != nil {
		return Output{}, err
	}
	catalogs, err := s.fetchCatalogs(ctx)
	if err != nil {
		return Output{}, err
	}
	out := Output{
		Coverage: "Published direct API list prices on the two official pricing pages. All page tables and numeric text are preserved. Page context includes other models, platform-specific conditions and worked examples; apply only applicable conditions. Negotiated/account-specific prices and linked external platform tariffs are outside this catalog.",
	}
	if input.All {
		out.Catalogs = catalogs
		return out, nil
	}
	for catalogIndex := range catalogs {
		catalog := &catalogs[catalogIndex]
		if catalog.Provider != provider {
			continue
		}
		for _, entry := range catalog.Models {
			if entry.ID != model {
				continue
			}
			out.Model, out.Provider, out.Currency = model, provider, catalog.Currency
			source := catalog.Source
			out.Source, out.PageContext, out.ContextNumbers = &source, catalog.PageContext, catalog.ContextNumbers
			out.Tables = append([]Table{}, entry.Tables...)
			for _, table := range catalog.Tables {
				if modelColumn(table.Headers) < 0 {
					out.Tables = append(out.Tables, table)
				}
			}
			return out, nil
		}
	}
	return Output{}, fmt.Errorf(
		"model-cost: model %q is not published in the live %s pricing catalog; use --all for exact IDs (snapshot dates are never guessed)",
		input.Model,
		provider,
	)
}

// Identity validates a request and returns its canonical model and provider.
func Identity(input Input) (string, string, error) {
	id := strings.ToLower(strings.TrimSpace(input.Model))
	if input.All {
		if id != "" {
			return "", "", errors.New("model-cost: choose a model ID or --all, not both")
		}
		return "", "", nil
	}
	if id == "" {
		return "", "", errors.New("model-cost: a model ID is required unless --all")
	}
	if len(id) > 160 || !regexp.MustCompile(`^[a-z0-9][a-z0-9./_-]*$`).MatchString(id) {
		return "", "", errors.New("model-cost: provide a full model ID using letters, digits, dots and hyphens")
	}
	if prefix, rest, found := strings.Cut(id, "/"); found {
		if prefix != providerOpenAI && prefix != providerAnthropic {
			return "", "", fmt.Errorf("model-cost: unsupported provider %q; supported: openai, anthropic", prefix)
		}
		id = rest
		if (prefix == providerAnthropic) != strings.HasPrefix(id, "claude-") {
			return "", "", errors.New("model-cost: provider prefix does not match the full model ID")
		}
	}
	if strings.HasPrefix(id, "claude-") {
		return strings.ReplaceAll(id, ".", "-"), providerAnthropic, nil
	}
	for _, prefix := range []string{"gpt-", "chatgpt-", "chat-", "o1", "o3", "o4", "text-", "tts-", "whisper-", "dall-e-", "omni-", "davinci-", "babbage-", "sora-", "codex-"} {
		if strings.HasPrefix(id, prefix) {
			return id, providerOpenAI, nil
		}
	}
	return "", "", errors.New(
		"model-cost: unsupported or ambiguous model; provide a full model ID (claude-opus-5-5 or gpt-6.1-sol); supported providers: anthropic, openai",
	)
}

func (s *Fetcher) fetchCatalogs(ctx context.Context) ([]Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	catalogs := make([]Catalog, 0, 2)
	for _, provider := range []struct{ name, url string }{{providerAnthropic, claudeURL}, {providerOpenAI, openAIURL}} {
		source, err := s.fetch(ctx, provider.url)
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

func (s *Fetcher) fetch(ctx context.Context, sourceURL string) (Source, error) {
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
		SHA256:    fmt.Sprintf("%x", sha256.Sum256(body)),
		Markdown:  text,
	}, nil
}

var (
	numberPattern     = regexp.MustCompile(`\d+(?:,\d{3})*(?:\.\d+)?`)
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
				numbers := numberPattern.FindAllString(cell, -1)
				if numbers == nil {
					numbers = []string{}
				}
				row[j] = Cell{Text: cell, Numbers: numbers}
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
	catalog.ContextNumbers = numberPattern.FindAllString(catalog.PageContext, -1)
	if catalog.ContextNumbers == nil {
		catalog.ContextNumbers = []string{}
	}
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
