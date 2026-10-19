package costsink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	apiRequestEvent = "api_request"
	llmRequestSpan  = "claude_code.llm_request"
	timeLayoutMS    = "2006-01-02T15:04:05.000Z"
)

// LogHalf is the cost half of one API call: an api_request log record, reduced to the fields a
// ledger row carries. Identity attributes (user, account, organization) are never read.
type LogHalf struct {
	RequestID, SessionID, TS, QuerySource, Model                    string
	InputTokens, OutputTokens, CacheReadTokens, CacheCreationTokens int64
	CostMicros                                                      *int64
	Speed, Effort, TraceID, SpanID                                  string
}

// SpanHalf is the agent half of one API call: a claude_code.llm_request span.
type SpanHalf struct {
	RequestID, SessionID, AgentID, ParentAgentID, QuerySource, Model string
	InputTokens, OutputTokens, CacheReadTokens, CacheCreationTokens  int64
	Speed, Effort, EndTS, TraceID, SpanID                            string
}

type otlpValue struct {
	StringValue *string         `json:"stringValue"`
	IntValue    json.RawMessage `json:"intValue"`
	DoubleValue *float64        `json:"doubleValue"`
}

type otlpAttr struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

// attrs indexes one record's attributes by key.
type attrs map[string]otlpValue

func indexAttrs(list []otlpAttr) attrs {
	out := make(attrs, len(list))
	for _, a := range list {
		out[a.Key] = a.Value
	}
	return out
}

func (a attrs) str(key string) string {
	if v, ok := a[key]; ok && v.StringValue != nil {
		return *v.StringValue
	}
	return ""
}

// integer reads an int64 attribute however the exporter spelled it: OTLP/JSON writes int64 as a
// decimal string, the observed Claude Code exporter as a JSON number, a double as a rounded float.
func (a attrs) integer(key string) (int64, bool) {
	v, ok := a[key]
	if !ok {
		return 0, false
	}
	if raw := bytes.Trim(bytes.TrimSpace(v.IntValue), `"`); len(raw) > 0 && string(raw) != "null" {
		if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			return n, true
		}
	}
	if v.DoubleValue != nil && !math.IsNaN(*v.DoubleValue) && !math.IsInf(*v.DoubleValue, 0) {
		return int64(math.Round(*v.DoubleValue)), true
	}
	if v.StringValue != nil {
		if n, err := strconv.ParseInt(*v.StringValue, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// costMicros is the call's price in micro-dollars: cost_usd_micros, else the cost_usd double an
// exporter without the integer field sends, rounded to the micro.
func (a attrs) costMicros() (int64, bool) {
	if micros, ok := a.integer("cost_usd_micros"); ok {
		return micros, true
	}
	v, ok := a["cost_usd"]
	if !ok || v.DoubleValue == nil || math.IsNaN(*v.DoubleValue) || math.IsInf(*v.DoubleValue, 0) {
		return 0, false
	}
	return int64(math.Round(*v.DoubleValue * 1e6)), true
}

func (a attrs) count(key string) int64 {
	n, _ := a.integer(key)
	return n
}

type otlpResource struct {
	Attributes []otlpAttr `json:"attributes"`
}

type logsRequest struct {
	ResourceLogs []struct {
		Resource  otlpResource `json:"resource"`
		ScopeLogs []struct {
			LogRecords []struct {
				TraceID      string          `json:"traceId"`
				SpanID       string          `json:"spanId"`
				TimeUnixNano json.RawMessage `json:"timeUnixNano"`
				Attributes   []otlpAttr      `json:"attributes"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

type tracesRequest struct {
	ResourceSpans []struct {
		Resource   otlpResource `json:"resource"`
		ScopeSpans []struct {
			Spans []struct {
				Name            string          `json:"name"`
				TraceID         string          `json:"traceId"`
				SpanID          string          `json:"spanId"`
				EndTimeUnixNano json.RawMessage `json:"endTimeUnixNano"`
				Attributes      []otlpAttr      `json:"attributes"`
			} `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

// scopedAttrs gives a record's attributes precedence over its resource defaults.
func scopedAttrs(resource otlpResource, record []otlpAttr) attrs {
	a := indexAttrs(resource.Attributes)
	for _, item := range record {
		a[item.Key] = item.Value
	}
	return a
}

func (a attrs) requestID() string {
	return firstFilled(a.str("request_id"), a.str("gen_ai.response.id"))
}

func (a attrs) sessionID() string {
	return firstFilled(a.str("session.id"), a.str("session_id"))
}

// DecodeLogs reduces an OTLP/HTTP JSON logs body to api_request records. Missing identity
// remains empty: the sink accounts for these observations without inventing attribution.
func DecodeLogs(body []byte) ([]LogHalf, error) {
	var request logsRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode OTLP logs: %w", err)
	}
	var halves []LogHalf
	for _, resource := range request.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			for _, record := range scope.LogRecords {
				a := scopedAttrs(resource.Resource, record.Attributes)
				if a.str("event.name") != apiRequestEvent {
					continue
				}
				half := LogHalf{
					RequestID: a.requestID(),
					SessionID: a.sessionID(),
					TraceID:   record.TraceID, SpanID: record.SpanID,
					TS: firstFilled(normalizeTimestamp(a.str("event.timestamp")), nanoTimestamp(record.TimeUnixNano)),
					QuerySource: a.str(
						"query_source",
					),
					Model:        a.str("model"),
					Speed:        a.str("speed"),
					Effort:       a.str("effort"),
					InputTokens:  a.count("input_tokens"),
					OutputTokens: a.count("output_tokens"),
					CacheReadTokens: a.count(
						"cache_read_tokens",
					),
					CacheCreationTokens: a.count("cache_creation_tokens"),
				}
				if micros, ok := a.costMicros(); ok {
					half.CostMicros = &micros
				}
				halves = append(halves, half)
			}
		}
	}
	return halves, nil
}

// RefusedError reports records refused by ingest for an unsafe session identity.
// Accepted halves have already been joined; the POST is not a bad request.
type RefusedError struct {
	Count  int
	Reason string
}

func (e *RefusedError) Error() string { return e.Reason }

// DecodeSpans reduces an OTLP/HTTP JSON traces body to its claude_code.llm_request spans, the
// query source spelled as the log half spells it.
func DecodeSpans(body []byte) ([]SpanHalf, error) {
	var request tracesRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode OTLP traces: %w", err)
	}
	var halves []SpanHalf
	for _, resource := range request.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				if span.Name != llmRequestSpan {
					continue
				}
				a := scopedAttrs(resource.Resource, span.Attributes)
				half := SpanHalf{
					RequestID: a.requestID(),
					SessionID: a.sessionID(),
					TraceID:   span.TraceID, SpanID: span.SpanID,
					AgentID: a.str("agent_id"),
					ParentAgentID: a.str(
						"parent_agent_id",
					),
					QuerySource:  NormalizeSpanQuerySource(a.str("query_source_safe")),
					Model:        a.str("model"),
					Speed:        a.str("speed"),
					Effort:       a.str("effort"),
					InputTokens:  a.count("input_tokens"),
					OutputTokens: a.count("output_tokens"),
					CacheReadTokens: a.count(
						"cache_read_tokens",
					),
					CacheCreationTokens: a.count("cache_creation_tokens"),
					EndTS:               nanoTimestamp(span.EndTimeUnixNano),
				}
				halves = append(halves, half)
			}
		}
	}
	return halves, nil
}

// NormalizeSpanQuerySource spells a span's query_source_safe as the log's query_source is spelled:
// the span writes `agent.builtin.x` where the log writes `agent:builtin:x`.
func NormalizeSpanQuerySource(source string) string {
	rest, ok := strings.CutPrefix(source, "agent.")
	if !ok {
		return source
	}
	kind, name, ok := strings.Cut(rest, ".")
	if !ok {
		return "agent:" + rest
	}
	return "agent:" + kind + ":" + name
}

// normalizeTimestamp writes an RFC 3339 instant as UTC with milliseconds; text it cannot parse is
// returned empty so the row falls back to the span's end or the receipt time instead of copying an
// unknown form.
func normalizeTimestamp(text string) string {
	if text == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(timeLayoutMS)
}

func nanoTimestamp(raw json.RawMessage) string {
	text := string(bytes.Trim(bytes.TrimSpace(raw), `"`))
	nanos, err := strconv.ParseInt(text, 10, 64)
	if err != nil || nanos <= 0 {
		return ""
	}
	return time.Unix(0, nanos).UTC().Format(timeLayoutMS)
}
