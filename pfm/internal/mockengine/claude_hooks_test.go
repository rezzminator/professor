package mockengine

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestClaudeHookGoldens(t *testing.T) {
	fix := newFixture(t)
	capture := filepath.Join(fix.root, "payload.json")
	hooks := map[string]any{}
	for _, event := range []string{hookSessionStart, hookUserPromptSubmit, hookPreToolUse, hookPostToolUse, hookPostToolUseFailure, hookPermissionRequest, hookNotification, hookSubagentStart, hookSubagentStop, hookStop, "StopFailure", hookSessionEnd} {
		hooks[event] = commandHook(`cat > "$MOCK_ENGINE_HOOK_PAYLOAD"`)
	}
	set, _, err := decodeHookDocument(mustJSON(t, map[string]any{"hooks": hooks}), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	proc := &process{
		ctx:    context.Background(),
		engine: engineClaude,
		cwd:    fix.work,
		env:    fix.env(nil),
		pid:    os.Getpid(),
		script: newScript(Scenario{NoTranscript: true}, engineClaude),
		stderr: io.Discard,
	}
	session := &claudeSession{
		proc:       proc,
		sessionID:  fixtureSession,
		transcript: filepath.Join(fix.root, "main.jsonl"),
		hooks:      set,
		environ:    append(os.Environ(), "MOCK_ENGINE_HOOK_PAYLOAD="+capture),
	}
	check := func(golden string, act func() error) {
		t.Helper()
		t.Run(golden, func(t *testing.T) {
			if err := act(); err != nil {
				t.Fatal(err)
			}
			assertClaudeGolden(t, capture, golden, fix.work, session.transcript)
		})
	}
	check("SessionStart", func() error {
		_, e := session.fire(
			hookSessionStart,
			"startup",
			func() hookPayload { p := session.payload(hookSessionStart); p.Source = "startup"; return p }(),
		)
		return e
	})
	check("UserPromptSubmit", func() error { _, e := session.prompt("inspect atlas"); return e })
	input := json.RawMessage(`{"command":"pwd"}`)
	pre := session.payload(hookPreToolUse)
	pre.ToolName, pre.ToolInput, pre.ToolUseID = "Bash", input, "toolu_fixture"
	check("PreToolUse", func() error { _, e := session.fire(hookPreToolUse, "Bash", pre); return e })
	check("PostToolUse", func() error { return session.postToolUse("Bash", input, "toolu_fixture", 0) })
	check(
		"PostToolUseFailure",
		func() error { _, e := session.tool(Step{Tool: "Bash", Input: input, Error: "Exit code 1"}); return e },
	)
	delete(session.hooks, hookNotification)
	check(
		"PermissionRequest",
		func() error { return session.requestAttention(Step{Type: StepPermission, Tool: "Bash", Input: input}) },
	)
	session.hooks[hookNotification] = set[hookPreToolUse]
	check("Notification-subagent-permission", func() error {
		return session.notification(Step{AgentID: "agent-fixture", NotificationType: "permission_prompt"})
	})
	check(
		"SubagentStart",
		func() error { _, e := session.background(Step{Name: "rr", AgentID: "agent-fixture"}); return e },
	)
	check("SubagentStop", func() error {
		_, e := session.background(Step{Name: "rr", AgentID: "agent-fixture", Status: "completed"})
		return e
	})
	session.lastReply = "done"
	check("Stop", session.stop)
	check("StopFailure", func() error { return session.turnError(Step{Error: "fixture API failure"}) })
	check("SessionEnd", func() error { return session.finish("other") })
}

func assertClaudeGolden(t *testing.T, capture, golden, cwd, transcript string) {
	t.Helper()
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := json.Unmarshal(raw, &actual); err != nil {
		t.Fatal(err)
	}
	if actual["cwd"] == cwd {
		actual["cwd"] = "/fixture/atlas"
	}
	if actual["transcript_path"] == transcript {
		actual["transcript_path"] = "/fixture/claude/main.jsonl"
	}
	if value, ok := actual["agent_transcript_path"].(string); ok {
		actual["agent_transcript_path"] = strings.Replace(
			value,
			strings.TrimSuffix(transcript, ".jsonl"),
			"/fixture/claude/main",
			1,
		)
	}
	if value, ok := actual["tool_use_id"].(string); ok && strings.HasPrefix(value, "toolu_") &&
		len(value) > len("toolu_") {
		actual["tool_use_id"] = "toolu_fixture"
	}
	if response, ok := actual["tool_response"].(map[string]any); ok {
		if response["stdout"] == cwd+"\n" {
			response["stdout"] = "/fixture/atlas\n"
		}
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Claude hook golden directory")
	}
	expectedPath := filepath.Join(filepath.Dir(source), "testdata", "hooks", "claude", golden+".json")
	expectedRaw, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err := json.Unmarshal(expectedRaw, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s: payload=%s, want %s", expectedPath, mustJSON(t, actual), expectedRaw)
	}
}
