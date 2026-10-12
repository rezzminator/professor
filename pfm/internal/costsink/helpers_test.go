package costsink

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	testSession = "00000000-0000-4000-8000-0000000000a1"
	testStart   = "2026-10-09T18:27:28.025Z"
)

func str(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func num(key string, value int64) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"intValue": value}}
}

// logBody is one OTLP/HTTP JSON logs POST holding the given records' attribute lists.
func logBody(records ...[]map[string]any) []byte {
	var recs []map[string]any
	for _, attrs := range records {
		recs = append(recs, map[string]any{"attributes": attrs})
	}
	return mustJSON(map[string]any{"resourceLogs": []any{map[string]any{
		"scopeLogs": []any{map[string]any{"logRecords": recs}},
	}}})
}

func apiRequest(session, request, source string, cost int64) []map[string]any {
	return []map[string]any{
		str("event.name", "api_request"), str("session.id", session), str("request_id", request),
		str("event.timestamp", testStart), str("query_source", source), str("model", "claude-opus-5-5"),
		num("input_tokens", 2), num("output_tokens", 323), num("cache_read_tokens", 76447),
		num("cache_creation_tokens", 15924), num("cost_usd_micros", cost), str("speed", "normal"),
		str("effort", "medium"), str("user.email", "user@example.invalid"),
	}
}

func spanBody(spans ...map[string]any) []byte {
	return mustJSON(map[string]any{"resourceSpans": []any{map[string]any{
		"scopeSpans": []any{map[string]any{"spans": spans}},
	}}})
}

func llmSpan(session, request, agent, parent, source string) map[string]any {
	attrs := []map[string]any{
		str("session.id", session), str("request_id", request), str("query_source_safe", source),
		str("model", "claude-opus-5-5"), str("speed", "normal"), str("effort", "medium"),
		num("input_tokens", 2), num("output_tokens", 323), num("cache_read_tokens", 76447),
		num("cache_creation_tokens", 15924), str("user.email", "user@example.invalid"),
	}
	if agent != "" {
		attrs = append(attrs, str("agent_id", agent))
	}
	if parent != "" {
		attrs = append(attrs, str("parent_agent_id", parent))
	}
	end := time.Date(2026, 10, 9, 18, 27, 28, 25_178_291, time.UTC)
	return map[string]any{
		"name": "claude_code.llm_request", "startTimeUnixNano": "1", "endTimeUnixNano": jsonString(end.UnixNano()),
		"attributes": attrs,
	}
}

func jsonString(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func post(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body []byte,
	header map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range header {
		request.Header.Set(k, v)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func gzipped(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func contains(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func chtimes(path string, when time.Time) error { return os.Chtimes(path, when, when) }

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
