package run

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

func TestRunOpenCodeCollectsAllAssistantIDsAndAggregatesUsage(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeOpenCodeStub(t, opencodeValidEvents())
	schema := json.RawMessage(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)
	result, err := Run(context.Background(), Request{
		Config:  opencodeMachine(binary, opencodeHome(t)),
		Engine:  pfmengine.OpenCode,
		TempDir: t.TempDir(),
		Prompt:  "hello",
		Schema:  schema,
		Env:     testEnv(capture, "CAPTURE_DIR="+capture, "PFM_TEST_TWO_ASSISTANTS=1"),
	})
	if err != nil {
		t.Fatalf("OpenCode multi-assistant Run() error = %v", err)
	}
	if result.Answer != `{"answer":"second"}` {
		t.Fatalf("answer = %q, want final structured answer", result.Answer)
	}
	if result.Usage == nil || result.Usage.Input != 10 || result.Usage.Output != 8 || result.Usage.CachedInput != 10 ||
		result.Usage.CacheCreation != 14 {
		t.Fatalf("aggregated usage = %#v, want input=10 output=8 cached=10 cache_creation=14", result.Usage)
	}
	if result.TotalCostUSD == nil || *result.TotalCostUSD != 0.375 {
		t.Fatalf("aggregated cost = %#v, want 0.375", result.TotalCostUSD)
	}
	if gets := capturedLines(t, filepath.Join(capture, "assistant-gets")); len(gets) < 4 {
		t.Fatalf(
			"schema collector did not wait for complete snapshots for both assistant IDs: %d GET(s), want at least 4",
			len(gets),
		)
	}
}

func TestParseOpenCodeJSONLRejectsMalformedAndIncompleteResults(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed", body: "not-json", want: "parse OpenCode JSONL"},
		{name: "missing terminal", body: `{"type":"text","part":{"text":"answer"}}`, want: "missing terminal"},
		{
			name: "tool error",
			body: `{"type":"tool_use","part":{"tool":"bash","state":{"status":"error","error":"denied"}}}
{"type":"step_finish","part":{"reason":"stop"}}`,
			want: "StructuredOutput",
		},
		{name: "schema without structured output", body: `{"type":"text","part":{"text":"answer"}}
{"type":"step_finish","part":{"reason":"stop"}}`, want: "StructuredOutput"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := Result{Stdout: testCase.body}
			err := parseOpenCodeJSONL(
				&result,
				Request{Engine: pfmengine.OpenCode, Schema: json.RawMessage(`{"type":"object"}`)},
			)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("parseOpenCodeJSONL() error = %v, want %q", err, testCase.want)
			}
		})
	}
}
