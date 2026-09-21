package run

import (
	"encoding/json"
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
