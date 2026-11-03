package hookentry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
		payload, skipped, err := readHerdrPayload(strings.NewReader(test.raw))
		if err != nil || skipped != nil {
			t.Fatal(err, skipped)
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
		skipped   string
	}{
		{"nested response", `{"tool_response":{"items":[null,true,2,{"escaped":"a\"b"}]},"session_id":"s1"}`, false, ""},
		{"huge ignored exponent", `{"tool_response":1e9999,"session_id":"s1"}`, false, ""},
		{"truncated response", `{"tool_response":{"items":[1,2]`, true, ""},
		{"bad escape in response", `{"tool_response":"\q"}`, true, ""},
		{"trailing object", `{"session_id":"s1"}{"agent_id":"child"}`, true, ""},
		{"wrong identity field type", `{"agent_id":1}`, true, ""},
		{
			"wrong label field types are skipped, the rest kept",
			`{"message":7,"is_interrupt":"yes","session_id":"s1"}`,
			false,
			"hook message: json: cannot unmarshal number into Go value of type string\n" +
				"hook is_interrupt: json: cannot unmarshal string into Go value of type bool",
		},
		{"null payload", `null`, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, skipped, err := readHerdrPayload(strings.NewReader(tc.raw))
			if (err != nil) != tc.bad || !tc.bad && payload.SessionID != "s1" {
				t.Fatalf("payload=%+v err=%v", payload, err)
			}
			gotSkipped := ""
			if skipped != nil {
				gotSkipped = skipped.Error()
			}
			if gotSkipped != tc.skipped {
				t.Fatalf("skipped = %q, want %q", gotSkipped, tc.skipped)
			}
		})
	}
}

func TestHerdrPromptActionAtTheReceivingDoor(t *testing.T) {
	for _, row := range []struct {
		event  string
		action string
	}{
		{"UserPromptSubmit", "chat.prompt"}, {"PostToolUse", ""},
	} {
		t.Run(row.event, func(t *testing.T) {
			rig := newChatRig(t)
			path := filepath.Join(t.TempDir(), "activity.jsonl")
			_, finish := obs.OpenLog(context.Background(), obs.Settings{
				Cmd: "internal", Path: path,
				Env: &paths.MapEnv{Values: map[string]string{obs.EnvBeat: "prompt", obs.EnvRun: "synthetic"}},
			})
			payload := `{"hook_event_name":"` + row.event + `","session_id":"s1","prompt":"hello"}`
			result := rig.run([]string{"working"}, payload)
			finish(0)
			if result.rc != 0 {
				t.Fatalf("hook result=%+v", result)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var recorder obs.Recorder
			if _, err := recorder.Write(raw); err != nil {
				t.Fatal(err)
			}
			var actions []obs.Record
			for _, record := range recorder.Records() {
				if _, exists := record.Field(obs.FieldAction); exists {
					actions = append(actions, record)
				}
			}
			if row.action == "" {
				if len(actions) != 0 {
					t.Fatalf("%s: tool report credited a prompt: %+v", path, actions)
				}
				return
			}
			if len(actions) != 1 {
				t.Fatalf("%s: actions=%+v, want one chat.prompt", path, actions)
			}
			for key, want := range map[string]string{obs.FieldAction: "chat.prompt", obs.FieldBeat: "prompt", obs.FieldRun: "synthetic", obs.FieldSession: "s1", obs.FieldDoor: "typed"} {
				if got, _ := actions[0].Field(key); got != want {
					t.Fatalf("%s: %s=%v, want %q", path, key, got, want)
				}
			}
		})
	}
}
