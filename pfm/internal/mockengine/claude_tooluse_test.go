package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
		if test.original != "" {
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
	capture, err := os.ReadFile(hookCapturePath(t))
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
	if got := readFile(t, path); got != "after" {
		t.Fatalf("Edit output=%q, want after", got)
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
				{
					Type:  StepToolCall,
					Tool:  "Bash",
					Input: json.RawMessage(`{"command":"true"}`),
				},
				{Type: StepTurn, Reply: "finished"},
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

func TestClaudeHeadlessToolStepFiresPostToolUse(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{BusyMS: intPtr(0), Steps: []Step{
		{
			Type:  StepToolCall,
			Tool:  "Bash",
			Input: json.RawMessage(`{"command":"true"}`),
		},
		{Type: StepTurn, Reply: "finished"},
	}})
	mark := filepath.Join(fix.recordDir, "post")
	writeHookSettings(t, filepath.Join(fix.work, ".claude", "settings.json"), map[string]any{
		hookPostToolUse: commandHook(`printf 'ran\n' > "` + mark + `"`),
	})
	code, _, stderr := runOnce(fix, "claude", []string{"-p", "--output-format", "json"}, "go")
	if code != 0 {
		t.Fatalf("headless exit %d stderr=%s", code, stderr)
	}
	if actual := readFile(t, mark); actual != "ran\n" {
		t.Fatalf("%s: headless post hook=%q, want ran\\n", mark, actual)
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
				{
					Type:  StepToolCall,
					Tool:  "Bash",
					Input: json.RawMessage(`{"command":"true"}`),
				},
				{Type: StepTurn, Reply: "finished"},
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
// so one that cannot finish (an async hook on a stalled store) never
// ends the turn nor keeps the event's other handlers from firing.
func TestClaudeInlineAsyncFailureNeitherEndsTheTurnNorSkipsLaterHandlers(t *testing.T) {
	for _, timeout := range []float64{0.2, 30} {
		t.Run(fmt.Sprintf("timeout-%g", timeout), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			marker := filepath.Join(fix.recordDir, "after-async")
			gate := filepath.Join(fix.recordDir, "blocked-hook")
			if err := syscall.Mkfifo(gate, 0o600); err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(fix.recordDir, "hook.pid")
			command := `printf '%s' "$$" > "` + pidFile + `"; read release < "` + gate + `"`
			settings := inlineHookSettings(t, map[string]any{
				hookUserPromptSubmit: []any{
					map[string]any{"hooks": []any{
						map[string]any{"type": "command", "command": command, "async": true, "timeout": timeout},
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
			pidText := waitFile(
				t,
				pidFile,
				func(content string) bool { _, err := strconv.Atoi(content); return err == nil },
			)
			pid, err := strconv.Atoi(pidText)
			if err != nil {
				t.Fatal(err)
			}
			if timeout == 30 {
				if err := syscall.Kill(pid, 0); err != nil {
					t.Fatalf("hook %d exited before session: %v", pid, err)
				}
			} else {
				deadline := time.NewTimer(3 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for syscall.Kill(pid, 0) == nil {
					select {
					case <-ticker.C:
					case <-deadline.C:
						t.Fatalf("hook %d still running or unreaped after timeout", pid)
					}
				}
			}
			session.typeLine("/exit")
			if code := session.waitExit(); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
			}
			if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
				t.Fatalf("hook %d after session exit: %v, want ESRCH (reaped)", pid, err)
			}
			if got := readFile(t, marker); got != "ran\n" {
				t.Fatalf("handler after the failed async one ran %q, want once", got)
			}
		})
	}
}

func TestClaudeInlineAsyncRunsWithoutApplyingItsAnswer(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	marker := filepath.Join(fix.recordDir, "async")
	gate := filepath.Join(fix.recordDir, "async-gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := os.OpenFile(gate, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release.Close() })
	command := `read release < "` + gate + `"; printf 'ran\n' >> "` + marker + `"; printf '%s\n' '{"decision":"block","reason":"async block","hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"async deny","additionalContext":"async context"}}'`
	settings := inlineHookSettings(t, map[string]any{
		hookSessionStart: []any{
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "async": true}}},
		},
		hookPreToolUse: []any{
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "async": true}}},
		},
	}, "")
	fix.write(
		Scenario{
			SessionID: fixtureSession,
			BusyMS:    intPtr(0),
			Steps: []Step{
				{Type: StepToolCall, Tool: "Bash", Input: json.RawMessage(`{"command":"true"}`)},
				{Type: StepTurn, Reply: "continued"},
			},
		},
	)
	session := fix.startTUI("claude", []string{"--settings", settings}, nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("%s: hook completed before release: %v", marker, err)
	}
	if _, err := release.WriteString("go\n"); err != nil {
		t.Fatal(err)
	}
	waitFile(t, marker, func(content string) bool { return content == "ran\n" })
	session.typeLine("go")
	session.waitFrame("continued", func(frame string) bool { return strings.Contains(frame, "continued") })
	if got := readFile(t, marker); got != "ran\n" {
		t.Fatalf("%s: tool hook completed before release: %q", marker, got)
	}
	if _, err := release.WriteString("go\n"); err != nil {
		t.Fatal(err)
	}
	waitFile(t, marker, func(content string) bool { return content == "ran\nran\n" })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	if got := readFile(t, marker); got != "ran\nran\n" {
		t.Fatalf("async runs=%q", got)
	}
	transcriptText := readFile(t, fix.claudeTranscript(fixtureSession))
	if strings.Contains(transcriptText, "async context") || strings.Contains(transcriptText, "async deny") ||
		strings.Contains(transcriptText, "async block") {
		t.Fatalf("async answer applied: %s", transcriptText)
	}
}

func TestClaudeAsyncExactCommandGateAllowsRenameBeforeRegistration(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.recordDir, "registration-hold")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	registered := filepath.Join(fix.recordDir, "registered.json")
	command := `cat > "` + registered + `"`
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), map[string]any{
		hookSessionStart: []any{
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "async": true}}},
		},
	})
	fix.write(Scenario{SessionID: fixtureSession})
	setScenarioField(
		t,
		fix,
		"hook_gates",
		[]map[string]string{{"event": hookSessionStart, "command": command, "until_gone": gate}},
	)
	settings := inlineHookSettings(t, map[string]any{hookSessionStart: commandHook(command)}, "")
	session := fix.startTUI(engineClaude, []string{"--settings", settings}, nil)
	session.waitFrame("composer before registration", func(frame string) bool { return strings.Contains(frame, "❯") })
	receipts := filepath.Join(fix.recordDir, "hook-gates.jsonl")
	waitFile(t, receipts, func(content string) bool { return strings.Contains(content, `"phase":"entered"`) })
	session.typeLine("/rename early name")
	waitFile(
		t,
		fix.claudeTranscript(fixtureSession),
		func(content string) bool { return strings.Contains(content, `"customTitle":"early name"`) },
	)
	if _, err := os.Stat(registered); !os.IsNotExist(err) {
		t.Fatalf("%s registered before release: %v", registered, err)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	raw := waitFile(t, registered, func(content string) bool { return json.Valid([]byte(content)) })
	var payload hookPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Event != hookSessionStart || payload.SessionID != fixtureSession {
		t.Fatalf("registration payload=%+v", payload)
	}
	waitFile(t, receipts, func(content string) bool { return strings.Count(content, "\n") == 2 })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	for index, line := range strings.Split(strings.TrimSpace(readFile(t, receipts)), "\n") {
		var record struct {
			Event    string      `json:"event"`
			Command  string      `json:"command"`
			Phase    string      `json:"phase"`
			Input    hookPayload `json:"input"`
			ExitCode int         `json:"exit_code"`
			Error    string      `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		wantPhase := []string{"entered", "completed"}[index]
		wantCode := []int{-1, 0}[index]
		if record.Event != hookSessionStart || record.Command != command || record.Phase != wantPhase ||
			record.Input.SessionID != fixtureSession ||
			record.ExitCode != wantCode ||
			record.Error != "" {
			t.Fatalf("%s: receipt=%+v want phase=%s exit=%d", receipts, record, wantPhase, wantCode)
		}
	}
}
