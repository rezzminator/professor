package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
		{name: "malformed", body: "not-json", want: "missing terminal"},
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

// One unreadable OpenCode event, part or value costs that item alone; a run that ends badly
// still carries the model's text, usage and cost beside its error.
func TestParseOpenCodeJSONLKeepsWhatItCouldRead(t *testing.T) {
	cost := func(value float64) *float64 { return &value }
	finish := func(reason string) string {
		return `{"type":"step_finish","part":{"reason":"` + reason + `","tokens":{"input":4,"output":2},"cost":0.25}}`
	}
	text := `{"type":"text","part":{"id":"p1","messageID":"m1","text":"kept"}}`
	cases := []struct {
		name        string
		body        string
		schema      bool
		wantErr     string
		answer      string
		usage       *TokenUsage
		cost        *float64
		diagnostics []string
	}{
		{
			name: "unreadable line, typeless event, text part and converter diagnostic",
			body: strings.Join([]string{
				"not-json", `{"part":{}}`, `{"type":"text","part":"stray"}`,
				`{"type":"pfm.unreadable","message":"could not read an OpenCode run message part: bad"}`,
				text, finish("stop"),
			}, "\n"),
			answer: "kept", usage: &TokenUsage{Input: 4, Output: 2}, cost: cost(0.25),
			diagnostics: []string{
				"could not read OpenCode JSONL event 1: invalid character 'o' in literal null (expecting 'u')",
				"could not read OpenCode JSONL event 2: the event has no type",
				"could not read OpenCode text event 3: json: cannot unmarshal string into Go value of type run.openCodeTextPart",
				"could not read an OpenCode run message part: bad",
			},
		},
		{
			name: "a text part with a mistyped time keeps its text",
			body: `{"type":"text","part":{"id":"p1","messageID":"m1","text":"kept","time":{"end":"soon"}}}` +
				"\n" + finish("stop"),
			answer: "kept", usage: &TokenUsage{Input: 4, Output: 2}, cost: cost(0.25),
			diagnostics: []string{
				"could not read OpenCode text event 1, whole; its readable fields were kept: json: cannot unmarshal string into Go struct field openCodeTextPart.time.end of type int64",
			},
		},
		{
			name: "a text part whose text is not a string keeps its JSON as the text",
			body: `{"type":"text","part":{"id":"p1","messageID":"m1","text":{"parts":["raw"]}}}` +
				"\n" + finish("stop"),
			answer: `{"parts":["raw"]}`, usage: &TokenUsage{Input: 4, Output: 2}, cost: cost(0.25),
			diagnostics: []string{
				"could not read OpenCode text event 1, whole; its readable fields were kept: json: cannot unmarshal object into Go struct field openCodeTextPart.text of type string",
			},
		},
		{
			name: "one step's tokens and cost leave the run's usage and cost unknown",
			body: strings.Join([]string{
				text,
				`{"type":"step_finish","part":{"reason":"tool-calls","tokens":{"input":-1},"cost":"x"}}`,
				finish("stop"),
			}, "\n"),
			answer: "kept",
			diagnostics: []string{
				"could not read OpenCode step_finish event 2 tokens: negative token count",
				"could not read OpenCode step_finish event 2 cost: json: cannot unmarshal string into Go value of type float64",
			},
		},
		{
			name:    "a reason other than stop keeps the text beside the error",
			body:    text + "\n" + finish("length"),
			wantErr: `OpenCode headless output ended with "length" and contains no structured result`,
			answer:  "kept", usage: &TokenUsage{Input: 4, Output: 2}, cost: cost(0.25),
		},
		{
			name:    "no reason keeps the text beside the error",
			body:    text + "\n" + `{"type":"step_finish","part":{"messageID":"m1"}}`,
			wantErr: "OpenCode headless output missing terminal stop event",
			answer:  "kept",
		},
		{
			name:    "an engine error keeps the text beside the error",
			body:    strings.Join([]string{text, `{"type":"error","error":{"message":"quota"}}`, finish("stop")}, "\n"),
			wantErr: "OpenCode headless output reported an error",
			answer:  "kept", usage: &TokenUsage{Input: 4, Output: 2}, cost: cost(0.25),
			diagnostics: []string{"quota"},
		},
		{
			name:    "a schema run without StructuredOutput keeps the text beside the error",
			body:    text + "\n" + finish("stop"),
			schema:  true,
			wantErr: "structured output missing or invalid: OpenCode did not return StructuredOutput",
			answer:  "kept", usage: &TokenUsage{Input: 4, Output: 2}, cost: cost(0.25),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := Result{Stdout: testCase.body}
			request := Request{Engine: pfmengine.OpenCode}
			if testCase.schema {
				request.Schema = json.RawMessage(`{"type":"object"}`)
			}
			err := parseOpenCodeJSONL(&result, request)
			if gotErr := fmt.Sprint(err); (err != nil || testCase.wantErr != "") && gotErr != testCase.wantErr {
				t.Fatalf("parseOpenCodeJSONL() error = %v, want %q", err, testCase.wantErr)
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

// A persisted part the converter cannot decode is carried into the normalized stream as a
// diagnostic; its sibling parts still convert and the snapshot still reads as complete.
func TestOpenCodeMessageConversionSkipsAnUnreadablePart(t *testing.T) {
	body := []byte(`{"info":{"role":"assistant","finish":"stop","time":{"completed":1}},"parts":[` +
		`"stray",{"type":"tool","tool":"StructuredOutput","state":"pending"},` +
		`{"type":"text","id":"p1","messageID":"m1","text":"kept"},{"type":"step-finish","reason":"stop"}]}`)
	request := Request{Engine: pfmengine.OpenCode}
	complete, err := openCodeMessageSnapshotComplete(body, request)
	if err != nil || !complete {
		t.Fatalf("openCodeMessageSnapshotComplete() = %v, %v; want true, nil", complete, err)
	}
	output, converted, err := openCodeMessageAsJSONL(body, request)
	if err != nil || !converted {
		t.Fatalf("openCodeMessageAsJSONL() = %v, %v; want complete, nil", converted, err)
	}
	result := Result{Stdout: output}
	if err := parseOpenCodeJSONL(&result, request); err != nil {
		t.Fatalf("parseOpenCodeJSONL(%s) error = %v", output, err)
	}
	if result.Answer != "kept" {
		t.Fatalf("answer = %q, want kept; stream %s", result.Answer, output)
	}
	want := []string{
		"could not read an OpenCode run message part: json: cannot unmarshal string into Go value of type run.openCodeSnapshotPart",
		"could not read an OpenCode run message part: json: cannot unmarshal string into Go struct field openCodeSnapshotPart.state of type struct { Status string \"json:\\\"status\\\"\" }",
	}
	if !reflect.DeepEqual(result.Diagnostics, want) {
		t.Fatalf("diagnostics = %#v, want %#v", result.Diagnostics, want)
	}
}

// The plugin writes the assistant marker atomically, so an unreadable one is read again on the
// next tick; only a wait that ends on it names the decode failure.
func TestCollectOpenCodeRunOutputReadsAnUnreadableMarkerAgain(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "assistants.json")
	if err := os.WriteFile(marker, []byte(`{"sessionID":`), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	collect := func(ctx context.Context, fake *clock.Fake) (<-chan string, <-chan error) {
		output, failure, done := make(chan string, 1), make(chan error, 1), make(chan struct{})
		go func() {
			got, err := collectOpenCodeRunOutput(ctx, "", "", nil, marker, Request{Clock: fake})
			output <- got
			failure <- err
			close(done)
		}()
		for fake.Pending() == 0 { // the collector waits on its first tick, unless it already returned
			select {
			case <-done:
				return output, failure
			case <-time.After(time.Millisecond):
			}
		}
		return output, failure
	}

	fake := clock.NewFake(time.Unix(0, 0))
	output, failure := collect(context.Background(), fake)
	if err := os.WriteFile(marker, []byte(`{"sessionID":"s","error":{"message":"quota"}}`), 0o600); err != nil {
		t.Fatalf("rewrite marker: %v", err)
	}
	fake.Advance(50 * time.Millisecond)
	if got, err := <-output, <-failure; err != nil || got != `{"error":{"message":"quota"},"type":"error"}`+"\n" {
		t.Fatalf("collect after the marker was rewritten = %q, %v", got, err)
	}

	if err := os.WriteFile(marker, []byte(`{"sessionID":`), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	fake = clock.NewFake(time.Unix(0, 0))
	output, failure = collect(ctx, fake)
	cancel()
	if got, err := <-output, <-failure; got != "" || err == nil ||
		err.Error() != "wait for OpenCode run result: context canceled\n"+
			"decode OpenCode assistant marker: unexpected end of JSON input" {
		t.Fatalf("collect on a marker that stays unreadable = %q, %v", got, err)
	}
}
