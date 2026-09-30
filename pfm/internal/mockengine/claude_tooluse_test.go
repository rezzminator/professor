package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

func callmeterCapturePath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate callmeter capture")
	}
	return filepath.Join(filepath.Dir(source), "..", "hookentry", "testdata", "callmeter", "scripted.jsonl")
}

func TestClaudeToolResponsesMatchCapturedKeysAndPfmReader(t *testing.T) {
	fix := newFixture(t)
	session := &claudeSession{proc: &process{cwd: fix.work}}
	path := filepath.Join(fix.work, "notes.md")
	capture, err := os.ReadFile(callmeterCapturePath(t))
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
		call := &callmeter.Call{}
		if err := callmeter.FileColumnsFromInput(call, test.tool, json.RawMessage(test.input), fix.work); err != nil {
			t.Fatal(err)
		}
		if err := callmeter.FileColumnsFromResult(call, test.tool, response); err != nil {
			t.Fatal(err)
		}
		if call.FilePath == nil || *call.FilePath != path ||
			call.FileBytesBefore == nil || *call.FileBytesBefore != int64(len(test.original)) {
			t.Fatalf("%s columns %+v", test.tool, call)
		}
	}
}

func TestClaudePostToolUseEdit(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	path := filepath.Join(fix.work, "note.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"file_path":"` + path + `","old_string":"before","new_string":"after"}`)
	record := filepath.Join(fix.recordDir, "post.jsonl")
	pre := filepath.Join(fix.recordDir, "pre.jsonl")
	transcriptCopy := filepath.Join(fix.recordDir, "transcript-at-post.jsonl")
	command := `cat >> "` + record + `"; printf '\n' >> "` + record + `"; cp "` +
		fix.claudeTranscript(fixtureSession) + `" "` + transcriptCopy + `"`
	writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), map[string]any{
		hookPreToolUse:  commandHook(`cat >> "` + pre + `"; printf '\n' >> "` + pre + `"`),
		hookPostToolUse: commandHook(command),
	})
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepToolCall, Tool: "Edit", Input: input}, {Type: StepTurn, Reply: "finished"},
	}})
	s := fix.startTUI("claude", claudeArgs(), nil)
	s.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	s.typeLine("go")
	s.waitFrame("reply", func(frame string) bool { return strings.Contains(frame, "finished") })
	var payload map[string]any
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(fix.recordDir, "post.jsonl"))), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["hook_event_name"] != hookPostToolUse || payload["tool_name"] != "Edit" {
		t.Fatalf("payload %v", payload)
	}
	capture, err := os.ReadFile(callmeterCapturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	for _, line := range strings.Split(string(capture), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) == nil && event["hook_event_name"] == hookPostToolUse &&
			event["tool_name"] == "Edit" && event["effort"] != nil {
			captured = event
			break
		}
	}
	if captured == nil {
		t.Fatal("captured Edit missing")
	}
	delete(captured, "prompt_id")
	delete(captured, "effort")
	for key := range captured {
		if _, ok := payload[key]; !ok {
			t.Errorf("payload lacks %s", key)
		}
	}
	for key := range payload {
		if _, ok := captured[key]; !ok {
			t.Errorf("payload has extra %s", key)
		}
	}
	var prePayload map[string]any
	if err := json.Unmarshal([]byte(readFile(t, pre)), &prePayload); err != nil {
		t.Fatal(err)
	}
	if payload["tool_use_id"] == "" || payload["tool_use_id"] != prePayload["tool_use_id"] ||
		payload["session_id"] != fixtureSession || payload["transcript_path"] != fix.claudeTranscript(fixtureSession) ||
		payload["cwd"] != fix.work || payload["permission_mode"] != "bypassPermissions" ||
		!reflect.DeepEqual(payload["tool_input"], prePayload["tool_input"]) || payload["duration_ms"].(float64) < 0 {
		t.Fatalf("post envelope %v pre %v", payload, prePayload)
	}
	if got := readFile(t, transcriptCopy); !strings.Contains(got, `"tool_use"`) ||
		strings.Contains(got, `"tool_result"`) {
		t.Fatalf("transcript at PostToolUse: %s", got)
	}
	response, ok := payload["tool_response"].(map[string]any)
	if !ok || response["originalFile"] != "before" {
		t.Fatalf("response %v", payload["tool_response"])
	}
	if got := readFile(t, path); got != "before" {
		t.Fatalf("disk changed: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, fix.claudeTranscript(fixtureSession))), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		message, ok := entry["message"].(map[string]any)
		if !ok {
			continue
		}
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			item, ok := part.(map[string]any)
			if ok && item["type"] == "tool_use" && item["id"] != payload["tool_use_id"] {
				t.Fatalf("transcript tool use %v, hook %v", item["id"], payload["tool_use_id"])
			}
		}
	}
}

func TestClaudePostToolUseAdmittedMatchersAndQuiet(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(map[bool]string{false: "admitted", true: "quiet"}[quiet], func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Quiet: quiet, Steps: []Step{
				{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "finished"},
			}})
			mark := func(label string) string {
				return `printf '%s\n' '` + label + `' >> "` + filepath.Join(fix.recordDir, "matchers") + `"`
			}
			entry := func(matcher, label string) any {
				return map[string]any{
					"matcher": matcher,
					"hooks":   []any{map[string]any{"type": "command", "command": mark(label)}},
				}
			}
			entries := []any{
				entry("Read", "read"), entry("*", "star"), entry("", "all"),
			}
			writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), map[string]any{
				hookPostToolUse: entries,
			})
			s := fix.startTUI("claude", claudeArgs(), nil)
			s.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			s.typeLine("go")
			s.waitFrame("reply", func(frame string) bool { return strings.Contains(frame, "finished") })
			path := filepath.Join(fix.recordDir, "matchers")
			if quiet {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("quiet hooks: %v", err)
				}
			} else if got := readFile(t, path); got != "star\nall\n" {
				t.Fatalf("matchers: %q", got)
			}
		})
	}
}

func TestClaudePostToolUseOtherToolResponse(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0), Steps: []Step{
		{Type: StepToolCall, Tool: "Read"}, {Type: StepTurn, Reply: "finished"},
	}})
	record := filepath.Join(fix.recordDir, "post.json")
	writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), map[string]any{
		hookPostToolUse: commandHook(`cat > "` + record + `"`),
	})
	s := fix.startTUI("claude", claudeArgs(), nil)
	s.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	s.typeLine("go")
	s.waitFrame("reply", func(frame string) bool { return strings.Contains(frame, "finished") })
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, record)), &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload["tool_response"]) != "{}" || string(payload["tool_name"]) != `"Read"` {
		t.Fatalf("other tool response: %s", readFile(t, record))
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

func TestClaudePostToolUseDeniedAndBrokenHandler(t *testing.T) {
	for _, test := range []struct {
		name, pre string
		wantExit  int
	}{
		{"deny-answer", `printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"blocked"}}'`, 0},
		{"deny-exit", `printf 'blocked\n' >&2; exit 2`, 0},
		{"broken-post", `true`, ExitUnpinned},
	} {
		t.Run(test.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
				{Type: StepToolCall, Tool: "Bash"}, {Type: StepTurn, Reply: "finished"},
			}})
			post := `printf 'post\n' >> "` + filepath.Join(fix.recordDir, "post") + `"`
			if test.name == "broken-post" {
				post = `printf '{not json'`
			}
			writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), map[string]any{
				hookPreToolUse: commandHook(test.pre), hookPostToolUse: commandHook(post),
			})
			s := fix.startTUI("claude", claudeArgs(), nil)
			s.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			s.typeLine("go")
			if test.wantExit == 0 {
				s.waitFrame("reply", func(frame string) bool { return strings.Contains(frame, "finished") })
				s.typeLine("/exit")
			}
			if code := s.waitExit(); code != test.wantExit {
				t.Fatalf("exit %d want %d stderr=%s", code, test.wantExit, s.stderr.String())
			}
			if test.wantExit == 0 {
				if _, err := os.Stat(filepath.Join(fix.recordDir, "post")); !os.IsNotExist(err) {
					t.Fatalf("denied post: %v", err)
				}
				if !strings.Contains(readFile(t, fix.claudeTranscript(fixtureSession)), `"is_error":true`) {
					t.Fatal("denial missing in transcript")
				}
			} else if !strings.Contains(s.stderr.String(), "PostToolUse") {
				t.Fatalf("stderr %s", s.stderr.String())
			}
		})
	}
}

// Real Claude runs an async handler in the background and ignores its outcome,
// so one that cannot finish (pfm's async callmeter on a stalled store) never
// ends the turn nor keeps the event's other handlers from firing.
func TestClaudeInlineAsyncFailureNeitherEndsTheTurnNorSkipsLaterHandlers(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	marker := filepath.Join(fix.recordDir, "after-async")
	settings := inlineHookSettings(t, map[string]any{
		hookUserPromptSubmit: []any{
			map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": "sleep 5", "async": true, "timeout": 0.2},
				map[string]any{"type": "command", "command": `printf 'ran\n' >> "` + marker + `"`},
			}},
		},
	}, "")
	fix.write(Scenario{
		SessionID: fixtureSession,
		BusyMS:    intPtr(0),
		Steps:     []Step{{Type: StepTurn, Reply: "continued"}},
	})
	session := fix.startTUI("claude", []string{"--settings", settings}, nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	session.waitFrame("continued", func(frame string) bool { return strings.Contains(frame, "continued") })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	if got := readFile(t, marker); got != "ran\n" {
		t.Fatalf("handler after the failed async one ran %q, want once", got)
	}
}
