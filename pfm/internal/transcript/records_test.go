package transcript

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The fixture is one agent transcript across a compaction: msg_A split over
// two lines, msg_B, a <synthetic> line, an unbilled line, the compact_boundary
// and its summary, msg_C, req_D carrying only a request id, a malformed line
// and a half-written tail. Four requests: A, B, C, D.
func TestRequestCountSpansACompaction(t *testing.T) {
	got, err := RequestCount(filepath.Join("testdata", "compacted-agent.jsonl"))
	if err != nil {
		t.Fatalf("RequestCount: %v", err)
	}
	if got != 4 {
		t.Fatalf("RequestCount = %d, want 4 (msg_A, msg_B, msg_C, req_D)", got)
	}
	if _, err := RequestCount(filepath.Join(t.TempDir(), "absent.jsonl")); err == nil {
		t.Fatal("RequestCount of an absent transcript returned no error")
	}
}

func TestCallCapByAgentType(t *testing.T) {
	for agentType, want := range map[string]struct {
		limit int
		role  string
	}{
		"flights-foreman": {250, "foreman"}, "foreman-pro": {250, "foreman"},
		"Flights-Lander": {200, "lander"}, "flights-lander": {200, "lander"},
		"flights-mechanical-executor": {150, ""}, "tracer": {150, ""}, "": {150, ""},
	} {
		if got := CallCap(agentType); got != want.limit {
			t.Errorf("CallCap(%q) = %v, want %v", agentType, got, want.limit)
		}
		if got := CallCapRole(agentType); got != want.role {
			t.Errorf("CallCapRole(%q) = %q, want %q", agentType, got, want.role)
		}
	}
}

// TestRequestTallyResumesWhereItStopped splits the compacted fixture after its
// compaction boundary: the resumed read takes only the appended bytes and ends
// at RequestCount's count; a transcript shorter than the tally, or rewritten
// under its head, is counted afresh.
func TestRequestTallyResumesWhereItStopped(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "compacted-agent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	split := 0
	for range 8 {
		split += bytes.IndexByte(fixture[split:], '\n') + 1
	}
	path := filepath.Join(t.TempDir(), "agent-a1.jsonl")
	if err := os.WriteFile(path, fixture[:split], 0o644); err != nil {
		t.Fatal(err)
	}
	var tally RequestTally
	first, read, err := tally.Resume(path)
	if err != nil || first != 2 || read != int64(split) || tally.Offset != int64(split) {
		t.Fatalf(
			"first Resume = %d, read %d, offset %d, %v; want 2 (msg_A, msg_B), %d, %d",
			first,
			read,
			tally.Offset,
			err,
			split,
			split,
		)
	}
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	second, read, err := tally.Resume(path)
	if err != nil || second != 4 || read != int64(len(fixture)-split) {
		t.Fatalf(
			"resumed Resume = %d, read %d, %v; want 4 from the %d appended bytes alone",
			second,
			read,
			err,
			len(fixture)-split,
		)
	}
	for _, test := range []struct {
		name    string
		content []byte
		want    int
	}{
		{"a shorter transcript restarts", fixture[:split], 2},
		{"a rewritten head restarts", bytes.ReplaceAll(fixture, []byte("msg_A"), []byte("msg_Q")), 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			resumed := tally
			if err := os.WriteFile(path, test.content, 0o644); err != nil {
				t.Fatal(err)
			}
			got, read, err := resumed.Resume(path)
			if err != nil || got != test.want || read != int64(len(test.content)) {
				t.Fatalf(
					"Resume = %d, read %d, %v; want %d from all %d bytes",
					got,
					read,
					err,
					test.want,
					len(test.content),
				)
			}
			if _, ok := resumed.Keys["message:msg_A"]; ok && test.want == 4 {
				t.Fatalf("keys %v keep the rewritten transcript's msg_A", resumed.Keys)
			}
		})
	}
	// A tally damaged outside Resume (a negative offset or line count) is
	// counted from the start, never left failing on every later call.
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, damaged := range []RequestTally{
		{Offset: -1, Head: tally.Head},
		{Offset: tally.Offset, Lines: -1, Head: tally.Head},
	} {
		offset, lines := damaged.Offset, damaged.Lines
		got, read, err := damaged.Resume(path)
		if err != nil || got != 4 || read != int64(len(fixture)) {
			t.Fatalf(
				"damaged tally offset %d lines %d: Resume = %d, read %d, %v; want 4 from all %d bytes",
				offset,
				lines,
				got,
				read,
				err,
				len(fixture),
			)
		}
	}
	// A whole request the writer has not yet ended with its newline counts now,
	// stays out of the tally, and counts once when its newline lands.
	request := `{"type":"assistant","requestId":"req_T","message":{"id":"msg_T",` +
		`"model":"claude-opus-5-5","usage":{"input_tokens":3,"output_tokens":5}}}`
	var unended RequestTally
	for _, step := range []struct {
		content   string
		want      int
		offset    int64
		persisted bool
	}{
		{string(fixture[:split]) + request, 3, int64(split), false},
		{string(fixture[:split]) + request + "\n", 3, int64(split + len(request) + 1), true},
	} {
		if err := os.WriteFile(path, []byte(step.content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, _, err := unended.Resume(path)
		if err != nil || got != step.want || unended.Offset != step.offset ||
			unended.Keys["message:msg_T"] != step.persisted {
			t.Fatalf(
				"Resume = %d, offset %d, msg_T kept %t, %v; want %d, %d, %t",
				got, unended.Offset, unended.Keys["message:msg_T"], err, step.want, step.offset, step.persisted,
			)
		}
	}
}

func TestResponseKeyNamespacesEachForm(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		message, request, uuid, at string
		want                       string
	}{
		{"the message id wins", "msg_1", "req_1", "u-1", "f:1", "message:msg_1"},
		{"else the request id", "", "req_1", "u-1", "f:1", "request:req_1"},
		{"else the uuid", "", "", "u-1", "f:1", "uuid:u-1"},
		{"else where it sits", "", "", "", "f:1", "at:f:1"},
		{"nothing to key on", "", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResponseKey(tc.message, tc.request, tc.uuid, tc.at); got != tc.want {
				t.Fatalf("ResponseKey = %q, want %q", got, tc.want)
			}
		})
	}
	if ResponseKey("", "x", "", "") == ResponseKey("x", "", "", "") {
		t.Fatal("a request id keyed equal to the same string as a message id")
	}
}

func TestCodexTokenInfoDecodesBothUsagesAndTheirAbsence(t *testing.T) {
	var info CodexTokenInfo
	line := `{"total_token_usage":{"input_tokens":50,"cached_input_tokens":20,"output_tokens":9,"reasoning_output_tokens":4,"total_tokens":59},` +
		`"last_token_usage":{"input_tokens":30,"total_tokens":31},"model_context_window":272000}`
	if err := json.Unmarshal([]byte(line), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := CodexTokenUsage{
		InputTokens:           50,
		CachedInputTokens:     20,
		OutputTokens:          9,
		ReasoningOutputTokens: 4,
		TotalTokens:           59,
	}
	if info.TotalTokenUsage == nil || *info.TotalTokenUsage != want || info.LastTokenUsage.InputTokens != 30 ||
		info.ModelContextWindow != 272000 {
		t.Fatalf("decoded %+v / %+v", info.TotalTokenUsage, info.LastTokenUsage)
	}
	var bare CodexTokenInfo
	if err := json.Unmarshal([]byte(`{"model_context_window":1}`), &bare); err != nil {
		t.Fatalf("decode bare: %v", err)
	}
	if bare.TotalTokenUsage != nil || bare.LastTokenUsage.Total() != 0 || info.LastTokenUsage.Total() != 31 {
		t.Fatalf("an absent usage must stay nil and total 0: %+v", bare)
	}
}
