package run

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// A 60 KB attachment is billed almost entirely as cache WRITE on its first call. When
// cache_creation_input_tokens was dropped, the receipt reported single-digit input tokens for it —
// a number small enough to look like a working measurement and wrong enough to mislead every
// reader of it. Pins the class: every input-billing field Claude reports survives into the receipt.
func TestParseTokenUsageKeepsEveryInputBillingFieldClaudeReports(t *testing.T) {
	usage, err := parseTokenUsage(json.RawMessage(`{
		"input_tokens": 2,
		"cached_input_tokens": 0,
		"cache_creation_input_tokens": 15842,
		"output_tokens": 4678
	}`))
	if err != nil {
		t.Fatalf("parseTokenUsage: %v", err)
	}
	if usage == nil {
		t.Fatal("parseTokenUsage returned no usage for a populated Claude usage block")
	}
	if usage.CacheCreation != 15842 {
		t.Fatalf("cache creation tokens = %d, want 15842", usage.CacheCreation)
	}
	body, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	if !strings.Contains(string(body), `"cache_creation_input_tokens":15842`) {
		t.Fatalf("the receipt's usage block dropped the cache-write tokens: %s", body)
	}
	alias, err := parseTokenUsage(json.RawMessage(`{"cache_write_input_tokens": 7}`))
	if err != nil || alias == nil || alias.CacheCreation != 7 {
		t.Fatalf("cache_write_input_tokens alias not honoured: %+v (%v)", alias, err)
	}
}

func TestClaudeModelUsageSumsEveryTurnNotTheLast(t *testing.T) {
	stdout := `{"result":"ok","usage":{"input_tokens":1947,"output_tokens":300,"cache_read_input_tokens":0},` +
		`"modelUsage":{"claude-a":{"inputTokens":2000,"outputTokens":6000,"cacheReadInputTokens":50000,"cacheCreationInputTokens":8000},` +
		`"claude-b":{"inputTokens":10,"outputTokens":20,"cacheReadInputTokens":0,"cacheCreationInputTokens":0}},"total_cost_usd":0.41,"is_error":false}`
	result := &Result{Stdout: stdout}
	if err := parseOutput(result, Request{Engine: pfmengine.Claude}); err != nil {
		t.Fatalf("parseOutput: %v", err)
	}
	got := *result.Usage
	want := TokenUsage{Input: 2010, CachedInput: 50000, CacheCreation: 8000, Output: 6020}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	// no modelUsage: the last-turn block stands, as before
	result = &Result{
		Stdout: `{"result":"ok","usage":{"input_tokens":1947,"output_tokens":300},"total_cost_usd":0.1,"is_error":false}`,
	}
	if err := parseOutput(result, Request{Engine: pfmengine.Claude}); err != nil {
		t.Fatalf("parseOutput: %v", err)
	}
	if result.Usage.Input != 1947 {
		t.Fatalf("usage without modelUsage = %+v", *result.Usage)
	}
}

// One unreadable item in an engine's output costs that item, never the run: the answer survives,
// the one value pfm could not read stays unknown, and the loss is a diagnostic.
func TestParseOutputKeepsTheAnswerPastAnUnreadableItem(t *testing.T) {
	cost := func(value float64) *float64 { return &value }
	cases := []struct {
		name        string
		engine      pfmengine.ID
		stdout      string
		answer      string
		usage       *TokenUsage
		cost        *float64
		diagnostics []string
	}{
		{
			name:   "Claude result that is not a string",
			engine: pfmengine.Claude,
			stdout: `{"result":123,"total_cost_usd":0.5}`,
			answer: "123",
			cost:   cost(0.5),
			diagnostics: []string{
				"could not read Claude result as text, so the answer is its JSON: json: cannot unmarshal number into Go value of type string",
			},
		},
		{
			name:   "Claude usage",
			engine: pfmengine.Claude,
			stdout: `{"result":"plain","usage":"bad","total_cost_usd":0.5}`,
			answer: "plain",
			cost:   cost(0.5),
			diagnostics: []string{
				"could not read Claude usage: json: cannot unmarshal string into Go value of type map[string]jsontext.Value",
			},
		},
		{
			name:        "Claude modelUsage leaves no last-turn usage standing",
			engine:      pfmengine.Claude,
			stdout:      `{"result":"plain","usage":{"input_tokens":4},"modelUsage":{"m":{"inputTokens":-1}}}`,
			answer:      "plain",
			diagnostics: []string{"could not read Claude modelUsage: modelUsage m: negative token count"},
		},
		{
			name:   "Claude cost",
			engine: pfmengine.Claude,
			stdout: `{"result":"plain","usage":{"input_tokens":4},"total_cost_usd":"free"}`,
			answer: "plain",
			usage:  &TokenUsage{Input: 4},
			diagnostics: []string{
				"could not read Claude cost: json: cannot unmarshal string into Go value of type float64",
			},
		},
		{
			name:   "Codex line, typeless event and item",
			engine: pfmengine.Codex,
			stdout: "{\"type\":\"thread.started\"}\nnot json\n{\"item\":{}}\n" +
				`{"type":"item.completed","item":"text"}` + "\n" +
				`{"type":"item.completed","item":{"type":"agent_message","text":"kept"}}` + "\n" +
				`{"type":"turn.completed","usage":{"input_tokens":4}}`,
			answer: "kept",
			usage:  &TokenUsage{Input: 4},
			diagnostics: []string{
				"could not read Codex JSONL event 2: invalid character 'o' in literal null (expecting 'u')",
				"could not read Codex JSONL event 3: the event has no type",
				"could not read Codex completed item in event 4: json: cannot unmarshal string into Go value of type struct { Type string \"json:\\\"type\\\"\"; Text string \"json:\\\"text\\\"\"; Message string \"json:\\\"message\\\"\" }",
			},
		},
		{
			name:   "Codex usage",
			engine: pfmengine.Codex,
			stdout: `{"type":"item.completed","item":{"type":"agent_message","text":"kept"}}` + "\n" +
				`{"type":"turn.completed","usage":{"input_tokens":-4}}`,
			answer:      "kept",
			diagnostics: []string{"could not read Codex usage in event 2: input_tokens must be nonnegative"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := &Result{Stdout: testCase.stdout}
			if err := parseOutput(result, Request{Engine: testCase.engine}); err != nil {
				t.Fatalf("parseOutput(%s) error = %v, want the answer kept", testCase.stdout, err)
			}
			if result.Answer != testCase.answer {
				t.Fatalf("answer = %q, want %q", result.Answer, testCase.answer)
			}
			if !reflect.DeepEqual(result.Usage, testCase.usage) {
				t.Fatalf("usage = %+v, want %+v", result.Usage, testCase.usage)
			}
			if !reflect.DeepEqual(result.TotalCostUSD, testCase.cost) {
				t.Fatalf("cost = %v, want %v", result.TotalCostUSD, testCase.cost)
			}
			if !reflect.DeepEqual(result.Diagnostics, testCase.diagnostics) {
				t.Fatalf("diagnostics = %#v, want %#v", result.Diagnostics, testCase.diagnostics)
			}
		})
	}
}
