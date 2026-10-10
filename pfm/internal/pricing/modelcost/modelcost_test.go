package modelcost

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

type modelCostTransport func(*http.Request) (*http.Response, error)

func (f modelCostTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const claudePage = `# Pricing
All prices are in USD.
## Model pricing
| Model | Base input tokens | 5m cache writes | 1h cache writes | Cache hits and refreshes | Output tokens |
| --- | --- | --- | --- | --- | --- |
| Claude Opus 5.5 | $4 / MTok | $5 / MTok | $8 / MTok | $0.20 / MTok<sup>1</sup> | $20 / MTok |
| Claude Opus 4.1 | $15 / MTok | $18.75 / MTok | $30 / MTok | $1.50 / MTok | $75 / MTok |
| Claude Haiku 5.5 (for prompts up to 100,000 tokens) | $0.10 / MTok | $0.125 / MTok | $0.20 / MTok | $0.01 / MTok | $0.50 / MTok |
| Claude Haiku 5.5 (for prompts over 100,000 tokens) | $0.50 / MTok | $0.625 / MTok | $1 / MTok | $0.05 / MTok | $2.50 / MTok |
| Claude Sonnet 5 | $3 / MTok | $3.75 / MTok | $6 / MTok | - | $15 / MTok |
## Fast mode pricing
| Model | Input | Output |
| --- | --- | --- |
| Claude Opus 5.5 / Claude Opus 5 | $8 / MTok | $40 / MTok |
## Batch processing
| Model | Batch input | Batch output |
| --- | --- | --- |
| Claude Opus 5.5 | $2 / MTok | $10 / MTok |
## Prompt caching
| Cache operation | Multiplier | Duration |
| --- | --- | --- |
| 5-minute write | 1.25x | 5 minutes |
US-only inference incurs a 1.1x multiplier. Batch discounts 50%.
`

const openAIHeader = "| Model | Short context input | Short context cached input | Short context cache writes | Short context output | " +
	"Long context input | Long context cached input | Long context cache writes | Long context output |\n" +
	"| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n"

const openAIPage = `# Pricing
Prices per 1M tokens.
Standard
### Standard pricing data
` + openAIHeader + `| gpt-6-astra | $10.00 | $1.00 | $12.50 | $50.00 | $20.00 | $2.00 | $25.00 | $75.00 |
| gpt-6-astra-pro | $90.00 | - | - | $400.00 | - | - | - | - |
Batch
### Batch pricing data
` + openAIHeader + `| gpt-6-astra | $5.00 | $0.50 | $6.25 | $25.00 | $10.00 | $1.00 | $12.50 | $37.50 |
Short context: ≤272K input tokens. Long context: >272K input tokens.
### Grouped Pricing Table data
| Model | Modality | Input | Cached input | Output |
| --- | --- | --- | --- | --- |
| gpt-audio-9 | Audio | $32.00 | $0.40 | $64.00 |
Specialized models
Prices per 1M tokens.
Standard
### Grouped Pricing Table data
| Category | Model | Input | Cached input | Output |
| --- | --- | --- | --- | --- |
| Codex | gpt-5.3-codex | $1.75 | $0.175 | $14.00 |
Fast
### Grouped Pricing Table data
| Category | Model | Input | Cached input | Output |
| --- | --- | --- | --- | --- |
| Codex | gpt-5.3-codex | $3.50 | $0.35 | $28.00 |
### Tool pricing
| Tool | Price |
| --- | --- |
| Web search | $10.00 per 1,000 calls |
`

// pagesFetcher serves claude and openai bodies by host; transportErr fails
// every request.
func pagesFetcher(claude, openai string, status int, transportErr error) *Fetcher {
	return NewFetcher(&http.Client{Transport: modelCostTransport(func(r *http.Request) (*http.Response, error) {
		if transportErr != nil {
			return nil, transportErr
		}
		body := openai
		if r.URL.Host == "platform.claude.com" {
			body = claude
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {"text/markdown"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}, nil)
}

func TestFetchParsesBothPagesClaudeFirst(t *testing.T) {
	catalogs, err := pagesFetcher(claudePage, openAIPage, http.StatusOK, nil).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(catalogs) != 2 || catalogs[0].Provider != providerAnthropic || catalogs[1].Provider != providerOpenAI {
		t.Fatalf("catalogs = %d, want anthropic then openai", len(catalogs))
	}
	if catalogs[0].Source.URL != claudeURL || catalogs[1].Source.URL != openAIURL ||
		catalogs[1].Source.FetchedAt == "" {
		t.Fatalf("sources = %+v / %+v", catalogs[0].Source.URL, catalogs[1].Source)
	}
	ids := map[string]bool{}
	for _, model := range catalogs[0].Models {
		ids[model.ID] = true
	}
	if !ids["claude-haiku-5-5"] || !ids["claude-opus-5"] ||
		!strings.Contains(catalogs[1].PageContext, "Long context: >272K") {
		t.Fatalf("claude ids %v; openai page context %q", ids, catalogs[1].PageContext)
	}
}

func TestFetchErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		transportErr     error
	}{
		{"malformed row", strings.Replace(openAIPage, "$10.00 | $1.00", "$10.00", 1), "column count", 200, nil},
		{"empty body", "", "empty", 200, nil},
		{"challenge", "<html><title>Just a moment</title></html>", "markdown pricing", 200, nil},
		{"partial table layout", openAIPage + "\n| Model | Input |\n| --- | revised format |\n| gpt-next | $3 |\n", "table layout", 200, nil},
		{"partial model column", openAIPage + "\n| Model name | Input |\n| --- | --- |\n| gpt-next | $3 |\n", "model column", 200, nil},
		{"shape drift", "# Pricing\n\nNew layout without tables", "pricing tables", 200, nil},
		{"upstream", "down", "HTTP 503", 503, nil},
		{"transport", "", "fixture network failure", 0, errors.New("fixture network failure")},
		{"oversized", strings.Repeat("x", (1<<20)+1), "exceeds", 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pagesFetcher(claudePage, tc.body, tc.status, tc.transportErr).Fetch(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Fetch error = %v, want %q", err, tc.want)
			}
		})
	}
	label := strings.Replace(claudePage, "Claude Sonnet 5 |", "Unrecognized publisher label |", 1)
	if _, err := pagesFetcher(label, openAIPage, 200, nil).Fetch(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "model label") {
		t.Fatalf("Fetch error = %v, want the unsupported model label", err)
	}
}

// publishedPages are both official pricing pages exactly as they were served
// on 2026-10-11. The Claude page's Bash tool table prices its token overhead
// per generation range: "Claude 4.7 and later models and Claude Mythos
// Preview" and "Claude 4.6 and earlier models".
func publishedPages(t *testing.T) (claude, openai string) {
	t.Helper()
	pages := make([]string, 2)
	for i, name := range []string{"testdata/claude-pricing.md", "testdata/openai-pricing.md"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		pages[i] = string(content)
	}
	return pages[0], pages[1]
}

// The first cell of the Bash tool table's generation-range row and of the
// Model pricing table's Opus 4.6 row, each the first such cell on the page.
const (
	bashToolGroupRow = "| Claude 4.6 and earlier models "
	opusRow          = "| Claude Opus 4.6 "
)

// TestGenerationGroupRows: a generation-range row ("Claude 4.6 and earlier
// models") outside the Model pricing table names no model: the catalog keeps
// it in its table and attributes it to no model, and every price row derives
// as the page publishes it. Any other label naming no model, and a generation
// range inside the Model pricing table, still fail the catalog.
func TestGenerationGroupRows(t *testing.T) {
	claude, openai := publishedPages(t)
	f64 := func(v float64) *float64 { return &v }
	t.Run("the published pages refresh", func(t *testing.T) {
		catalogs, err := pagesFetcher(claude, openai, http.StatusOK, nil).Fetch(context.Background())
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		var bash *Table
		for i, table := range catalogs[0].Tables {
			if table.Heading == "Bash tool" {
				bash = &catalogs[0].Tables[i]
			}
		}
		if bash == nil || len(bash.Rows) != 2 || bash.Rows[1][0].Text != "Claude 4.6 and earlier models" ||
			bash.Rows[1][1].Text != "244 tokens" {
			t.Fatalf("Bash tool table = %+v, want both generation rows kept", bash)
		}
		for _, model := range catalogs[0].Models {
			for _, table := range model.Tables {
				if table.Heading == "Bash tool" && table.Rows[0][0].Text == "Claude 4.6 and earlier models" {
					t.Fatalf("model %s holds the generation-range row", model.ID)
				}
			}
		}
		prices, err := Derive(catalogs, "2026-10-11T00:00:00Z")
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		claudeRows := map[string]pricing.Row{}
		for _, row := range prices.Rows {
			if row.Engine == pricing.EngineClaude {
				claudeRows[row.Key] = row
			}
		}
		keys := []string{
			"claude-fable-5", "claude-fable-5-1", "claude-haiku-3-5", "claude-haiku-4-5", "claude-haiku-5-5",
			"claude-mythos-5", "claude-mythos-5-1", "claude-opus-4", "claude-opus-4-1", "claude-opus-4-5",
			"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5",
			"claude-sonnet-4", "claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-5", "claude-sonnet-5-5",
		}
		if len(claudeRows) != len(keys) {
			t.Fatalf("claude rows = %d, want %d: %v", len(claudeRows), len(keys), claudeRows)
		}
		for _, key := range keys {
			if _, ok := claudeRows[key]; !ok {
				t.Fatalf("no derived row %s", key)
			}
		}
		for key, want := range map[string]pricing.Rates{
			"claude-opus-4-6":   {In: f64(5), Out: f64(25), Hit: f64(0.5), W5m: f64(6.25), W1h: f64(10)},
			"claude-sonnet-4-6": {In: f64(3), Out: f64(15), Hit: f64(0.3), W5m: f64(3.75), W1h: f64(6)},
			"claude-sonnet-5":   {In: f64(2), Out: f64(10), Hit: f64(0.2), W5m: f64(2.5), W1h: f64(4)},
		} {
			if !reflect.DeepEqual(claudeRows[key].Rates, want) {
				t.Fatalf("%s rates = %+v, want %+v", key, claudeRows[key].Rates, want)
			}
		}
		if long := claudeRows["claude-haiku-5-5"].Long; long == nil || long.Above != 100000 || *long.In != 0.5 {
			t.Fatalf("claude-haiku-5-5 long tier = %+v, want above 100000 at $0.50 in", long)
		}
	})
	for _, tc := range []struct{ name, label string }{
		{"a bold generation range", "**Claude 4.6 and earlier models**"},
		{"a later range of a whole generation", "Claude 5 and later models"},
	} {
		t.Run(tc.name+" refreshes", func(t *testing.T) {
			page := strings.Replace(claude, bashToolGroupRow, "| "+tc.label+" ", 1)
			if page == claude {
				t.Fatal("the fixture edit matched nothing")
			}
			if _, err := pagesFetcher(page, openai, http.StatusOK, nil).Fetch(context.Background()); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
		})
	}
	for _, tc := range []struct{ name, page, want string }{
		{
			"an unknown label still fails",
			strings.Replace(claude, bashToolGroupRow, "| Unrecognized publisher label ", 1),
			`unsupported model label "Unrecognized publisher label"; catalog incomplete`,
		},
		{
			"a generation range in the Model pricing table fails",
			strings.Replace(claude, opusRow, "| Claude 4.6 and earlier models ", 1),
			`group label "Claude 4.6 and earlier models" in the Model pricing table names no model; catalog incomplete`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.page == claude {
				t.Fatal("the fixture edit matched nothing")
			}
			if _, err := pagesFetcher(tc.page, openai, http.StatusOK, nil).Fetch(context.Background()); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Fetch error = %v, want %q", err, tc.want)
			}
		})
	}
}
