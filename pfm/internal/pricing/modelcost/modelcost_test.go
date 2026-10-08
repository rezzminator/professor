package modelcost

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type modelCostTransport func(*http.Request) (*http.Response, error)

func (f modelCostTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const modelCostOpenAI = `# Pricing
Prices per 1M tokens. All prices USD.
Standard
### Standard pricing data
| Model | Short context input | Cached input | Cache writes | Output | Long context input |
| --- | --- | --- | --- | --- | --- |
| gpt-6.1-sol | $2.00 | $0.10 | $2.50 | $10.00 | $4.00 |
| gpt-6.1-sol-pro | $99.00 | - | - | $199.00 | - |
Batch
### Batch pricing data
| Model | Input | Output |
| --- | --- | --- |
| gpt-6.1-sol | $1.00 | $5.00 |
Fast
### Fast pricing data
| Model | Input | Output |
| --- | --- | --- |
| gpt-6.1-sol | $4.00 | $20.00 |
Ultrafast
### Ultrafast pricing data
| Model | Input | Output |
| --- | --- | --- |
| gpt-6.1-sol | $12.00 | $60.00 |
Short context: ≤272K input tokens. Long context: >272K input tokens.
Regional processing costs a 10% uplift through November 21, 2026.
### Tool pricing
| Tool | Price |
| --- | --- |
| Web search | $10.00 per 1,000 calls |
`

const modelCostClaude = `# Pricing
All prices are in USD.
## Model pricing
| Model | Base input tokens | 5m cache writes | 1h cache writes | Cache hits | Output tokens |
| --- | --- | --- | --- | --- | --- |
| Claude Opus 5.5 | $4 / MTok | $5 / MTok | $8 / MTok | $0.20 / MTok<sup>1</sup> | $20 / MTok |
| Claude Sonnet 4.5 | $3 / MTok | $3.75 / MTok | $6 / MTok | $0.30 / MTok | $15 / MTok |
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
| 1-hour write | 2x | 1 hour |
US-only inference incurs a 1.1x multiplier. Batch discounts 50%.
MTok means million tokens. Tool overhead is 346 tokens.
`

func modelCostFixture(t *testing.T, transport modelCostTransport) *Fetcher {
	t.Helper()
	return NewFetcher(&http.Client{Transport: transport}, nil)
}

func modelCostResponse(body string, status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"text/markdown"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func modelCostCall(t *testing.T, fetcher *Fetcher, model string) (map[string]any, string) {
	t.Helper()
	return modelCostCallInput(t, fetcher, map[string]any{"model": model})
}

func modelCostCallInput(t *testing.T, fetcher *Fetcher, arguments map[string]any) (map[string]any, string) {
	t.Helper()
	input := Input{}
	input.Model, _ = arguments["model"].(string)
	input.All, _ = arguments["all"].(bool)
	output, err := fetcher.Lookup(context.Background(), input)
	if err != nil {
		return nil, err.Error()
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, ""
}

func TestModelCostPublishedDetails(t *testing.T) {
	for _, tc := range []struct {
		name, model, body, provider string
		tables                      int
		want                        []string
	}{
		{"openai", "openai/gpt-6.1-sol", modelCostOpenAI, "openai", 5, []string{"2.00", "0.10", "2.50", "10.00", "4.00", "1.00", "5.00", "4.00", "20.00", "12.00", "60.00", "10.00", "1,000"}},
		{"claude", "anthropic/claude-opus-5-5", modelCostClaude, "anthropic", 4, []string{"4", "5", "8", "0.20", "1", "20", "8", "40", "2", "10", "1.25", "5", "2", "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			session := modelCostFixture(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cache-Control") != "no-cache, no-store" {
					t.Errorf("cache header = %q", r.Header.Get("Cache-Control"))
				}
				body := tc.body
				if tc.provider == "openai" && r.URL.Host == "platform.claude.com" {
					body = modelCostClaude
				}
				if tc.provider == "anthropic" && r.URL.Host == "developers.openai.com" {
					body = modelCostOpenAI
				}
				return modelCostResponse(body, 200), nil
			})
			out, failure := modelCostCall(t, session, tc.model)
			if failure != "" {
				t.Fatalf("expected published prices, got %s", failure)
			}
			if out["provider"] != tc.provider {
				t.Fatalf("provider = %v", out["provider"])
			}
			source := out["source"].(map[string]any)
			if source["markdown"] != tc.body || source["fetched_at"] == "" ||
				source["sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(tc.body))) {
				t.Fatalf("source evidence incomplete: %v", source)
			}
			tables := out["tables"].([]any)
			if len(tables) != tc.tables {
				t.Fatalf("tables = %d, want %d", len(tables), tc.tables)
			}
			var rates []string
			for _, table := range tables {
				for _, row := range table.(map[string]any)["rows"].([]any) {
					for _, cell := range row.([]any)[1:] {
						for _, n := range cell.(map[string]any)["numbers"].([]any) {
							rates = append(rates, n.(string))
						}
					}
				}
			}
			if !reflect.DeepEqual(rates, tc.want) {
				t.Fatalf("rate and condition numbers = %v, want %v", rates, tc.want)
			}
			if out["currency"] != "USD" || out["page_context"] == "" {
				t.Fatalf("currency/context missing: %v", out)
			}
			// The next lookup must see a publisher update and keep decimal spelling.
			changed := strings.ReplaceAll(tc.body, "$2.00", "$2.0010")
			next := tc.body
			reused := modelCostFixture(t, func(r *http.Request) (*http.Response, error) {
				body := next
				if tc.provider == "openai" && r.URL.Host == "platform.claude.com" {
					body = modelCostClaude
				}
				if tc.provider == "anthropic" && r.URL.Host == "developers.openai.com" {
					body = modelCostOpenAI
				}
				return modelCostResponse(body, 200), nil
			})
			_, failure = modelCostCall(t, reused, tc.model)
			if failure != "" {
				t.Fatal(failure)
			}
			next = changed
			updated, failure := modelCostCallInput(t, reused, map[string]any{"model": tc.model})
			if failure != "" || updated["source"].(map[string]any)["markdown"] != changed || calls != 2 {
				t.Fatalf("updated source = %v, failure %s, calls %d", updated, failure, calls)
			}
		})
	}
}

func TestModelCostPublishedWhisperID(t *testing.T) {
	body := modelCostOpenAI + "\n### Transcription prices\n| Model | Price per minute |\n| --- | --- |\n| Whisper | $0.006 / minute |\n"
	session := modelCostFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "platform.claude.com" {
			return modelCostResponse(modelCostClaude, 200), nil
		}
		return modelCostResponse(body, 200), nil
	})
	out, failure := modelCostCall(t, session, "whisper-1")
	if failure != "" || out["model"] != "whisper-1" {
		t.Fatalf("published Whisper API ID lookup = %v, failure=%s", out, failure)
	}
	tables := out["tables"].([]any)
	price := tables[0].(map[string]any)["rows"].([]any)[0].([]any)[1].(map[string]any)
	if price["text"] != "$0.006 / minute" || price["numbers"].([]any)[0] != "0.006" {
		t.Fatalf("published transcription price = %v", price)
	}
}

func TestModelCostErrors(t *testing.T) {
	for _, tc := range []struct {
		name, model, body, want string
		status                  int
		transportErr            error
	}{
		{"required", "", modelCostOpenAI, "model ID is required", 200, nil},
		{"bare alias", "sonnet", modelCostClaude, "full model ID", 200, nil},
		{"unknown provider", "other/model", modelCostOpenAI, "unsupported provider", 200, nil},
		{"unknown model", "gpt-fictional", modelCostOpenAI, "not published", 200, nil},
		{"false prefix", "gpt-6.1", modelCostOpenAI, "not published", 200, nil},
		{"unknown snapshot", "claude-opus-5-5-20990101", modelCostClaude, "not published", 200, nil},
		{"malformed row", "gpt-6.1-sol", strings.Replace(modelCostOpenAI, "$2.00 | $0.10", "$2.00", 1), "column count", 200, nil},
		{"empty body", "gpt-6.1-sol", "", "empty", 200, nil},
		{"challenge", "gpt-6.1-sol", "<html><title>Just a moment</title></html>", "markdown pricing", 200, nil},
		{"partial table layout", "gpt-6.1-sol", modelCostOpenAI + "\n| Model | Input |\n| --- | revised format |\n| gpt-next | $3 |\n", "table layout", 200, nil},
		{"partial model label", "claude-opus-5-5", strings.Replace(modelCostClaude, "Claude Sonnet 4.5", "Unrecognized publisher label", 1), "model label", 200, nil},
		{"partial model column", "gpt-6.1-sol", modelCostOpenAI + "\n| Model name | Input |\n| --- | --- |\n| gpt-next | $3 |\n", "model column", 200, nil},
		{"shape drift", "gpt-6.1-sol", "# Pricing\n\nNew layout without tables", "pricing tables", 200, nil},
		{"upstream", "gpt-6.1-sol", "down", "HTTP 503", 503, nil},
		{"transport", "gpt-6.1-sol", "", "fixture network failure", 0, errors.New("fixture network failure")},
		{"oversized", "gpt-6.1-sol", strings.Repeat("x", (1<<20)+1), "exceeds", 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := modelCostFixture(t, func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(tc.model, "claude-") && r.URL.Host == "developers.openai.com" {
					return modelCostResponse(modelCostOpenAI, 200), nil
				}
				if !strings.HasPrefix(tc.model, "claude-") && r.URL.Host == "platform.claude.com" {
					return modelCostResponse(modelCostClaude, 200), nil
				}
				if tc.transportErr != nil {
					return nil, tc.transportErr
				}
				return modelCostResponse(tc.body, tc.status), nil
			})
			_, failure := modelCostCall(t, session, tc.model)
			if !strings.Contains(failure, tc.want) {
				t.Fatalf("failure = %q, want %q", failure, tc.want)
			}
		})
	}
}
