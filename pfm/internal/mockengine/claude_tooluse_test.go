package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// hookCapturePath is the hook payloads a real Claude session sent, captured
// once: the shape the fake engine's tool responses must keep.
func hookCapturePath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate hook capture")
	}
	return filepath.Join(filepath.Dir(source), "testdata", "scripted.jsonl")
}

func TestClaudeToolResponsesMatchCapturedKeys(t *testing.T) {
	fix := newFixture(t)
	session := &claudeSession{proc: &process{cwd: fix.work}}
	path := filepath.Join(fix.work, "notes.md")
	capture, err := os.ReadFile(hookCapturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ tool, input, original string }{
		{"Write", `{"file_path":"` + path + `","content":"new"}`, ""},
		{"Edit", `{"file_path":"` + path + `","old_string":"old","new_string":"new"}`, "old"},
		{"Write", `{"file_path":"` + path + `","content":"replacement"}`, "old"},
	} {
		if test.tool == "Edit" {
			if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		response, err := session.toolResponse(test.tool, json.RawMessage(test.input))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(response, &got); err != nil {
			t.Fatal(err)
		}
		var captured map[string]any
		for _, line := range strings.Split(string(capture), "\n") {
			var event map[string]any
			if json.Unmarshal([]byte(line), &event) == nil &&
				event["hook_event_name"] == hookPostToolUse && event["tool_name"] == test.tool && event["effort"] != nil {
				captured = event
				break
			}
		}
		if captured == nil {
			t.Fatalf("capture missing %s", test.tool)
		}
		want := captured["tool_response"].(map[string]any)
		keys := func(m map[string]any) map[string]bool {
			out := map[string]bool{}
			for k := range m {
				out[k] = true
			}
			return out
		}
		if !reflect.DeepEqual(keys(got), keys(want)) {
			t.Fatalf("%s response keys %v want %v", test.tool, keys(got), keys(want))
		}
		if test.tool == "Write" {
			kind := "create"
			if test.original != "" {
				kind = "update"
			}
			if got["type"] != kind {
				t.Fatalf("Write response type %v, want %s", got["type"], kind)
			}
		}
	}
}

func TestClaudeHeadlessToolStepDoesNotFirePostToolUse(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0), Steps: []Step{
		{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "finished"},
	}})
	mark := filepath.Join(fix.recordDir, "post")
	writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), map[string]any{
		hookPostToolUse: commandHook(`printf 'ran\n' > "` + mark + `"`),
	})
	code, _, stderr := runOnce(fix, "claude", []string{"-p", "--output-format", "json"}, "go")
	if code != 0 {
		t.Fatalf("headless exit %d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(mark); !os.IsNotExist(err) {
		t.Fatalf("headless post hook: %v", err)
	}
}

// toolResult is what a tool_result record says: its content and whether it is an error.
type toolResult struct {
	Content string
	IsError bool
}
