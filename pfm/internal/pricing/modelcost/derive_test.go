package modelcost

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

func rate(value float64) *float64 { return &value }

// catalogsOf parses the two pages as Fetch would.
func catalogsOf(t *testing.T, claude, openai string) []Catalog {
	t.Helper()
	anthropic, err := parseCatalog(providerAnthropic, Source{URL: claudeURL, FetchedAt: "x", Markdown: claude})
	if err != nil {
		t.Fatalf("parse claude page: %v", err)
	}
	openAI, err := parseCatalog(providerOpenAI, Source{URL: openAIURL, FetchedAt: "x", Markdown: openai})
	if err != nil {
		t.Fatalf("parse openai page: %v", err)
	}
	return []Catalog{anthropic, openAI}
}

func derived(t *testing.T, claude, openai string) pricing.Table {
	t.Helper()
	table, err := Derive(catalogsOf(t, claude, openai), "2026-10-08T09:00:00Z")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return table
}

func TestDeriveTheStandardTextTokenRows(t *testing.T) {
	want := pricing.Table{
		Version:   pricing.Version,
		FetchedAt: "2026-10-08T09:00:00Z",
		Sources:   []pricing.Source{{Provider: "anthropic", URL: claudeURL}, {Provider: "openai", URL: openAIURL}},
		Rows: []pricing.Row{
			{
				Key: "claude-haiku-5-5", Engine: pricing.EngineClaude,
				Rates: pricing.Rates{In: rate(0.1), Out: rate(0.5), Hit: rate(0.01), W5m: rate(0.125), W1h: rate(0.2)},
				Long: &pricing.Long{Above: 100000, Rates: pricing.Rates{
					In: rate(0.5), Out: rate(2.5), Hit: rate(0.05), W5m: rate(0.625), W1h: rate(1),
				}},
			},
			{
				Key: "claude-opus-4-1", Engine: pricing.EngineClaude,
				Rates: pricing.Rates{In: rate(15), Out: rate(75), Hit: rate(1.5), W5m: rate(18.75), W1h: rate(30)},
			},
			{
				Key: "claude-opus-5-5", Engine: pricing.EngineClaude,
				Rates: pricing.Rates{In: rate(4), Out: rate(20), Hit: rate(0.2), W5m: rate(5), W1h: rate(8)},
			},
			{
				Key: "claude-sonnet-5", Engine: pricing.EngineClaude,
				Rates: pricing.Rates{In: rate(3), Out: rate(15), W5m: rate(3.75), W1h: rate(6)},
			},
			{
				Key: "gpt-5.3-codex", Engine: pricing.EngineCodex,
				Rates: pricing.Rates{In: rate(1.75), Out: rate(14), Cached: rate(0.175)},
			},
			{
				Key: "gpt-6-astra", Engine: pricing.EngineCodex,
				Rates: pricing.Rates{In: rate(10), Out: rate(50), Cached: rate(1)},
				Long:  &pricing.Long{Above: 272000, Rates: pricing.Rates{In: rate(20), Out: rate(75), Cached: rate(2)}},
			},
			{
				Key: "gpt-6-astra-pro", Engine: pricing.EngineCodex,
				Rates: pricing.Rates{In: rate(90), Out: rate(400)},
			},
		},
	}
	got := derived(t, claudePage, openAIPage)
	if got.Version != want.Version || got.FetchedAt != want.FetchedAt || !pricing.SameRates(got, want) {
		gotJSON, _ := pricing.Encode(got)
		wantJSON, _ := pricing.Encode(want)
		t.Fatalf("Derive =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
	if _, err := pricing.Encode(got); err != nil {
		t.Fatalf("a derived table must be a valid prices.json: %v", err)
	}
}

func TestDeriveVariants(t *testing.T) {
	duplicate := func(input string) string {
		return openAIPage + "### Grouped Pricing Table data\n" + openAIHeader +
			"| gpt-6-astra | " + input + " | $1.00 | $12.50 | $50.00 | $20.00 | $2.00 | $25.00 | $75.00 |\n"
	}
	astraInput := func(cell string) string {
		return strings.Replace(openAIPage, "| gpt-6-astra | $10.00 |", "| gpt-6-astra | "+cell+" |", 1)
	}
	for _, tc := range []struct {
		name, claude, openai, key string
		in                        *float64 // nil: the key is unpriced
	}{
		{"batch, fast and audio tables never price", claudePage, openAIPage, "gpt-audio-9", nil},
		{"standard beats batch", claudePage, openAIPage, "gpt-6-astra", rate(10)},
		{"equal duplicate rows agree", claudePage, duplicate("$10.00"), "gpt-6-astra", rate(10)},
		{"conflicting duplicate rows are unpriced", claudePage, duplicate("$99.00"), "gpt-6-astra", nil},
		{"a dash duplicate conflicts", claudePage, duplicate("-"), "gpt-6-astra", nil},
		{
			"another prompt condition is skipped", strings.Replace(claudePage, "| Claude Sonnet 5 |",
				"| Claude Opus 9 (for prompts with images) | $1 / MTok | - | - | - | $2 / MTok |\n| Claude Sonnet 5 |", 1),
			openAIPage, "claude-opus-9", nil,
		},
		{
			"per 1M tokens spelling", strings.Replace(claudePage, "| Claude Opus 5.5 | $4 / MTok", "| Claude Opus 5.5 | $4 / 1M tokens", 1),
			openAIPage, "claude-opus-5-5", rate(4),
		},
		{
			"long columns without the page boundary", claudePage,
			strings.Replace(openAIPage, "Long context: >272K input tokens.", "", 1), "gpt-6-astra", nil,
		},
		{
			"a category table whose last mode line is Fast", claudePage,
			strings.Replace(openAIPage, "Prices per 1M tokens.\nStandard\n### Grouped", "Prices per 1M tokens.\nStandard\nPrior mode\nFast\n### Grouped", 1),
			"gpt-5.3-codex", nil,
		},
		{"missing input", claudePage, astraInput("-"), "gpt-6-astra", nil},
		{"not available", claudePage, astraInput("N/A"), "gpt-6-astra", nil},
		{"a range", claudePage, astraInput("$0.10–$0.20"), "gpt-6-astra", nil},
		{"another unit", claudePage, astraInput("$2 / 1K tokens"), "gpt-6-astra", nil},
		{"per image", claudePage, astraInput("$1 per image"), "gpt-6-astra", nil},
		{"not a number", claudePage, astraInput("$oops"), "gpt-6-astra", nil},
		{"thousands separator", claudePage, astraInput("$1,000.00"), "gpt-6-astra", rate(1000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, ok := derived(t, tc.claude, tc.openai).Resolve(tc.key)
			switch {
			case tc.in == nil && ok:
				t.Fatalf("%s priced at in=%v, want unpriced", tc.key, *row.In)
			case tc.in != nil && (!ok || *row.In != *tc.in):
				t.Fatalf("%s = %+v (ok %v), want in=%v", tc.key, row.Rates, ok, *tc.in)
			}
		})
	}
}

func TestDeriveRefusesAProviderWithoutRows(t *testing.T) {
	for _, tc := range []struct{ name, claude, openai, want string }{
		{"no prices-per-1M statement", claudePage, strings.ReplaceAll(openAIPage, "Prices per 1M tokens.", ""), "openai"},
		{"no model pricing table", strings.Replace(claudePage, "## Model pricing", "## Model rates", 1), openAIPage, "anthropic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Derive(catalogsOf(t, tc.claude, tc.openai), "2026-10-08T09:00:00Z")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Derive error = %v, want one naming %s", err, tc.want)
			}
		})
	}
	if _, err := Derive(catalogsOf(t, claudePage, openAIPage)[:1], "2026-10-08T09:00:00Z"); err == nil {
		t.Fatal("Derive of one provider succeeded, want an error")
	}
}
