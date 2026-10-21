package hookentry

import (
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Codex's memory consolidation thread fires hooks from the chat's own Codex
// process with "transcript_path": null; the run drops it before any tmux or
// Herdr call. Which payloads count as that thread is TestThreadWithoutRollout's.
func TestHerdrCodexThreadWithoutRolloutReportsNothing(t *testing.T) {
	rig := newChatRig(t)
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	before := rig.tmux.stored
	payload := `{"session_id":"m1","transcript_path":null,"cwd":"/h/.codex/memories"}`
	rig.wantQuiet(rig.run([]string{"--engine", "codex", "working"}, payload))
	if calls := rig.tmux.allCalls(); len(calls) != 0 {
		t.Fatalf("tmux calls = %q, want none for a thread with no rollout", calls)
	}
	rig.wantCalls()
	if rig.tmux.stored != before {
		t.Fatalf("stored = %q, want it untouched (was %q)", rig.tmux.stored, before)
	}
}

// The Codex thread probe reads JSON null only: a string, an absent key or a
// payload that is not an object all keep reporting.
func TestThreadWithoutRollout(t *testing.T) {
	for _, test := range []struct {
		engine string
		raw    string
		want   bool
	}{
		{"cx", `{"transcript_path":null}`, true},
		{"cx", `{"transcript_path": null }`, true},
		{"cx", `{"transcript_path":"/r.jsonl"}`, false},
		{"cx", `{"transcript_path":""}`, false},
		{"cx", `{}`, false},
		{"cl", `{"transcript_path":null}`, false},
	} {
		payload, err := readHerdrPayload(strings.NewReader(test.raw))
		if err != nil {
			t.Fatal(err)
		}
		if got := threadWithoutRollout(pfmengine.ID(test.engine), payload); got != test.want {
			t.Errorf("threadWithoutRollout(%s, %s) = %v, want %v", test.engine, test.raw, got, test.want)
		}
	}
}

func TestReadHerdrPayloadValidatesTheWholeStream(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		bad       bool
	}{
		{"nested response", `{"tool_response":{"items":[null,true,2,{"escaped":"a\"b"}]},"session_id":"s1"}`, false},
		{"huge ignored exponent", `{"tool_response":1e9999,"session_id":"s1"}`, false},
		{"truncated response", `{"tool_response":{"items":[1,2]`, true},
		{"bad escape in response", `{"tool_response":"\q"}`, true},
		{"trailing object", `{"session_id":"s1"}{"agent_id":"child"}`, true},
		{"wrong field type", `{"agent_id":1}`, true},
		{"null payload", `null`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := readHerdrPayload(strings.NewReader(tc.raw))
			if (err != nil) != tc.bad || !tc.bad && payload.SessionID != "s1" {
				t.Fatalf("payload=%+v err=%v", payload, err)
			}
		})
	}
}
