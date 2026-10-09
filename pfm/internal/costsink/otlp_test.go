package costsink

import (
	"encoding/json"
	"testing"
)

func TestDecodeLogsKeepsOnlyApiRequestWithTheLedgerFields(t *testing.T) {
	other := []map[string]any{
		str("event.name", "tool_result"),
		str("session.id", testSession),
		str("request_id", "req_x"),
	}
	body := logBody(other, apiRequest(testSession, "req_1", "repl_main_thread", 149149))
	halves, err := DecodeLogs(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(halves) != 1 {
		t.Fatalf("DecodeLogs kept %d records, want only the api_request", len(halves))
	}
	got := halves[0]
	want := LogHalf{
		RequestID: "req_1", SessionID: testSession, TS: testStart, QuerySource: "repl_main_thread",
		Model: "claude-opus-5-5", InputTokens: 2, OutputTokens: 323, CacheReadTokens: 76447,
		CacheCreationTokens: 15924, Speed: "normal", Effort: "medium",
	}
	if got.CostMicros == nil || *got.CostMicros != 149149 {
		t.Fatalf("CostMicros = %v, want 149149", got.CostMicros)
	}
	got.CostMicros = nil
	if got != want {
		t.Fatalf("LogHalf = %+v, want %+v", got, want)
	}
}

func TestDecodeReadsInt64FieldsAsNumbersOrStrings(t *testing.T) {
	attrs := apiRequest(testSession, "req_1", "repl_main_thread", 0)
	for i, a := range attrs {
		switch a["key"] {
		case "cost_usd_micros":
			attrs[i] = map[string]any{"key": "cost_usd_micros", "value": map[string]any{"intValue": "149149"}}
		case "output_tokens":
			attrs[i] = map[string]any{"key": "output_tokens", "value": map[string]any{"doubleValue": 323.0}}
		}
	}
	halves, err := DecodeLogs(logBody(attrs))
	if err != nil || len(halves) != 1 {
		t.Fatalf("DecodeLogs = %v, %v", halves, err)
	}
	if halves[0].CostMicros == nil || *halves[0].CostMicros != 149149 || halves[0].OutputTokens != 323 {
		t.Fatalf("half = %+v, want cost 149149 and output 323 from string and double forms", halves[0])
	}
	// An exporter that sends only the dollar double still prices the call: a null cost on a
	// log half would write a log+span row the contract allows only for span.
	for i, a := range attrs {
		if a["key"] == "cost_usd_micros" {
			attrs[i] = map[string]any{"key": "cost_usd", "value": map[string]any{"doubleValue": 0.14914940000000002}}
		}
	}
	halves, err = DecodeLogs(logBody(attrs))
	if err != nil || len(halves) != 1 {
		t.Fatalf("DecodeLogs = %v, %v", halves, err)
	}
	if halves[0].CostMicros == nil || *halves[0].CostMicros != 149149 {
		t.Fatalf("CostMicros = %v, want 149149 from cost_usd alone", halves[0].CostMicros)
	}
}

func TestDecodeLogsRefusesARecordWithoutAnIdentity(t *testing.T) {
	missing := apiRequest(testSession, "", "repl_main_thread", 5)
	halves, err := DecodeLogs(logBody(missing))
	if err == nil || len(halves) != 0 {
		t.Fatalf("DecodeLogs = %v, %v; want the record refused with an error", halves, err)
	}
}

func TestDecodeSpansKeepsOnlyLlmRequestAndTheEndTime(t *testing.T) {
	body := spanBody(
		map[string]any{"name": "claude_code.tool", "attributes": []map[string]any{str("session.id", testSession)}},
		llmSpan(testSession, "req_1", "a0000000000000001", "a0000000000000002", "agent.builtin.general-purpose"),
		llmSpan(testSession, "req_2", "", "", "repl_main_thread"),
	)
	halves, err := DecodeSpans(body)
	if err != nil || len(halves) != 2 {
		t.Fatalf("DecodeSpans = %v, %v; want the two llm_request spans", halves, err)
	}
	first := halves[0]
	if first.RequestID != "req_1" || first.AgentID != "a0000000000000001" ||
		first.ParentAgentID != "a0000000000000002" ||
		first.QuerySource != "agent:builtin:general-purpose" ||
		first.EndTS != "2026-10-09T18:27:28.025Z" {
		t.Fatalf("first span half = %+v", first)
	}
	if main := halves[1]; main.AgentID != "" || main.ParentAgentID != "" || main.QuerySource != "repl_main_thread" {
		t.Fatalf("main span half = %+v, want an empty agent and the source unchanged", main)
	}
}

func TestDecodeRejectsABodyThatIsNotJSON(t *testing.T) {
	if _, err := DecodeLogs([]byte("not json")); err == nil {
		t.Fatal("DecodeLogs accepted a non-JSON body")
	}
	if _, err := DecodeSpans([]byte(`{"resourceSpans":`)); err == nil {
		t.Fatal("DecodeSpans accepted a truncated body")
	}
	var empty json.RawMessage = []byte(`{}`)
	if halves, err := DecodeLogs(empty); err != nil || len(halves) != 0 {
		t.Fatalf("DecodeLogs({}) = %v, %v; want no halves, no error", halves, err)
	}
}

func TestSpanOnlyQuerySourceUsesTheLogSpelling(t *testing.T) {
	for in, want := range map[string]string{
		"agent.builtin.general-purpose": "agent:builtin:general-purpose",
		"agent.custom.my-agent":         "agent:custom:my-agent",
		"repl_main_thread":              "repl_main_thread",
		"web_search_tool":               "web_search_tool",
		"":                              "",
	} {
		if got := NormalizeSpanQuerySource(in); got != want {
			t.Errorf("NormalizeSpanQuerySource(%q) = %q, want %q", in, got, want)
		}
	}
}
