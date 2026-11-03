package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodexHookPayloadGoldens(t *testing.T) {
	fix := newFixture(t)
	channel := filepath.Join(fix.root, "action.json")
	writeAction := func(value string) {
		t.Helper()
		if err := os.WriteFile(channel, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeAction(`{"beat":"A","run":"synthetic-A"}`)
	captured := filepath.Join(fix.recordDir, "hooks.jsonl")
	environments := filepath.Join(fix.recordDir, "environments")
	events := []string{
		"SessionStart",
		"UserPromptSubmit",
		"PreToolUse",
		"PermissionRequest",
		"PostToolUse",
		"PreCompact",
		"PostCompact",
		"SubagentStart",
		"SubagentStop",
		"Stop",
		"Interrupt",
		"SessionEnd",
	}
	settings := map[string]any{}
	for _, event := range events {
		settings[event] = commandHook(
			`cat >> "` + captured + `"; printf '\n' >> "` + captured + `"; printf '%s:%s\n' "$PFM_BEAT" "$PFM_RUN" >> "` + environments + `"`,
		)
	}
	writeHookSettings(t, filepath.Join(fix.codexHome, "hooks.json"), settings)
	hooks, _, err := loadHookDocument(filepath.Join(fix.codexHome, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	proc := &process{
		ctx:    context.Background(),
		engine: engineCodex,
		env:    fix.env(nil),
		cwd:    fix.work,
		pid:    os.Getpid(),
		stdout: io.Discard,
		stderr: io.Discard,
		script: newScript(
			Scenario{ActionFile: channel, Jail: Jail{ProcRoot: fix.procRoot, SIDDir: fix.sidDir}},
			engineCodex,
		),
	}
	session := &codexSession{
		proc:      proc,
		codexHome: fix.codexHome,
		threadID:  fixtureThread,
		model:     DefaultCodexModel,
		hooks:     hooks,
		environ:   os.Environ(),
	}
	if err := session.start(false); err != nil {
		t.Fatal(err)
	}
	writeAction(`{"beat":"B","run":"synthetic-B"}`)
	if _, err := session.prompt("run the atlas check"); err != nil {
		t.Fatal(err)
	}
	step := Step{Type: "permission", Tool: "Bash", Input: json.RawMessage(`{"command":"printf atlas"}`)}
	if err := session.requestAttention(step); err != nil {
		t.Fatal(err)
	}
	if err := session.answerAttention(step, true); err != nil {
		t.Fatal(err)
	}
	if err := session.compact(Step{}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.background(Step{Name: "reviewer", Status: "reviewed"}); err != nil {
		t.Fatal(err)
	}
	if err := session.recordAssistant("approved", Tokens{}); err != nil {
		t.Fatal(err)
	}
	if err := session.stop(); err != nil {
		t.Fatal(err)
	}
	if err := session.interrupt(); err != nil {
		t.Fatal(err)
	}
	if err := session.finish("prompt_input_exit"); err != nil {
		t.Fatal(err)
	}
	if err := session.finish("other"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) != len(events) {
		t.Fatalf("%s: events=%s, want %v", captured, raw, events)
	}
	for index, event := range events {
		t.Run(event, func(t *testing.T) {
			var actual map[string]any
			if err := json.Unmarshal(lines[index], &actual); err != nil {
				t.Fatal(err)
			}
			if value, ok := actual["cwd"].(string); ok && value == fix.work {
				actual["cwd"] = "/fixture/atlas"
			}
			if value, ok := actual["transcript_path"].(string); ok &&
				strings.HasPrefix(value, filepath.Join(fix.codexHome, "sessions")+string(filepath.Separator)) &&
				strings.HasSuffix(value, "-"+fixtureThread+".jsonl") {
				actual["transcript_path"] = "/fixture/rollout.jsonl"
			}
			if value, ok := actual["turn_id"].(string); ok && value != "" {
				actual["turn_id"] = "turn-atlas"
			}
			if value, ok := actual["tool_use_id"].(string); ok && strings.HasPrefix(value, "call_") {
				actual["tool_use_id"] = "call_atlas"
			}
			if value, ok := actual["agent_id"].(string); ok && value != "" {
				actual["agent_id"] = "agent-atlas"
			}
			if value, ok := actual["agent_transcript_path"].(string); ok &&
				strings.HasPrefix(value, filepath.Join(fix.codexHome, "sessions")+string(filepath.Separator)) &&
				strings.HasSuffix(value, ".jsonl") {
				actual["agent_transcript_path"] = "/fixture/agent.jsonl"
			}
			golden := filepath.Join("testdata", "hooks", "codex", event+".json")
			expectedRaw, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			var expected map[string]any
			if err := json.Unmarshal(expectedRaw, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: actual=%s, want=%s", golden, mustJSON(t, actual), expectedRaw)
			}
		})
	}
	raw, err = os.ReadFile(environments)
	if err != nil {
		t.Fatal(err)
	}
	want := "A:synthetic-A\n" + strings.Repeat("B:synthetic-B\n", 11)
	if string(raw) != want {
		t.Fatalf("%s: attribution=%q, want %q", environments, raw, want)
	}
}

func TestCodexApprovalFlow(t *testing.T) {
	for _, row := range []struct{ name, key, decision, terminal, want string }{
		{name: "approve", key: "\r", terminal: "finished", want: "SessionStart\nUserPromptSubmit\nPreToolUse\nPermissionRequest\nPostToolUse\nStop\nSessionEnd\n"},
		{name: "deny", key: "\x1b", terminal: "Interrupted", want: "SessionStart\nUserPromptSubmit\nPreToolUse\nPermissionRequest\nInterrupt\nSessionEnd\n"},
		{name: "hook allow", decision: "allow", terminal: "finished", want: "SessionStart\nUserPromptSubmit\nPreToolUse\nPermissionRequest\nPostToolUse\nStop\nSessionEnd\n"},
		{name: "hook deny", decision: "deny", terminal: "Interrupted", want: "SessionStart\nUserPromptSubmit\nPreToolUse\nPermissionRequest\nInterrupt\nSessionEnd\n"},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			captured := filepath.Join(fix.recordDir, "events")
			settings := map[string]any{}
			for _, event := range []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "PreToolUse", "PostToolUse", "Stop", "Interrupt", "SessionEnd"} {
				command := `printf '%s\n' '` + event + `' >> "` + captured + `"`
				if event == "PermissionRequest" && row.decision != "" {
					command += `; printf '%s' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"` + row.decision + `"}}}'`
				}
				settings[event] = commandHook(command)
			}
			writeHookSettings(t, filepath.Join(fix.codexHome, "hooks.json"), settings)
			fix.write(
				Scenario{
					SessionID: fixtureThread,
					Pane:      codexShapes,
					Steps: []Step{
						{
							Type:    "permission",
							Tool:    "Bash",
							Message: "Approve atlas check",
							Input:   json.RawMessage(`{"command":"printf atlas"}`),
						},
						{Type: StepTurn, Reply: "finished"},
					},
				},
			)
			session := fix.startTUI("codex", codexArgs(), nil)
			session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
			session.typeLine("run the atlas check")
			if row.decision == "" {
				session.waitFrame(
					"approval",
					func(frame string) bool { return strings.Contains(frame, "Approve atlas check") },
				)
				session.typeRaw(row.key)
			}
			session.waitFrame(
				"approval result",
				func(frame string) bool { return strings.Contains(frame, row.terminal) },
			)
			session.typeLine("/exit")
			if code := session.waitExit(); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
			}
			actual, err := os.ReadFile(captured)
			if err != nil {
				t.Fatal(err)
			}
			if string(actual) != row.want {
				t.Fatalf("%s: events=%q, want %q", captured, actual, row.want)
			}
		})
	}
}

func TestCodexScriptedModelRequestGoldens(t *testing.T) {
	fix := newFixture(t)
	for _, event := range []string{"PreModelRequest", "PostModelRequest"} {
		path := filepath.Join("testdata", "hooks", "codex", event+".json")
		golden, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		captured := filepath.Join(fix.recordDir, event+".json")
		settings := map[string]any{event: commandHook(`cat > "` + captured + `"`)}
		writeHookSettings(t, filepath.Join(fix.codexHome, "hooks.json"), settings)
		hooks, _, err := loadHookDocument(filepath.Join(fix.codexHome, "hooks.json"))
		if err != nil {
			t.Fatal(err)
		}
		session := &codexSession{
			proc: &process{
				ctx:    context.Background(),
				cwd:    fix.work,
				env:    fix.env(nil),
				stderr: io.Discard,
				script: newScript(Scenario{}, engineCodex),
			},
			hooks:   hooks,
			environ: os.Environ(),
		}
		if err := session.hook(Step{Event: event, Payload: golden}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(captured)
		if err != nil {
			t.Fatal(err)
		}
		var actual, expected any
		if err := json.Unmarshal(raw, &actual); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(golden, &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s: captured=%s, want %s", path, raw, golden)
		}
		if err := session.hook(Step{Event: "wrong", Payload: golden}); err == nil {
			t.Fatalf("%s: accepted mismatched hook event", path)
		}
	}
}
