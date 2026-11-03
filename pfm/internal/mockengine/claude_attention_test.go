package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSubagentPermissionPromptWaitsForAnswer(t *testing.T) {
	for _, approved := range []bool{true, false} {
		t.Run(fmt.Sprint(approved), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(
				Scenario{
					SessionID: fixtureSession,
					Steps: []Step{
						{
							Type:    StepPermission,
							Tool:    "Write",
							Input:   json.RawMessage(`{"file_path":"report.txt","content":"approved report\n"}`),
							AgentID: "agent-fixture",
						},
						{Type: StepTurn, Reply: "permission answered"},
					},
				},
			)
			if err := os.WriteFile(filepath.Join(fix.work, "report.txt"), []byte("unchanged\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(fix.root, "events")
			notification := filepath.Join(fix.root, "notification.json")
			writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), map[string]any{
				hookPermissionRequest: commandHook("printf '%s\\n' PermissionRequest >> " + trace),
				hookNotification:      commandHook("cat > " + notification),
				hookPostToolUse:       commandHook("printf '%s\\n' PostToolUse >> " + trace),
				hookStop:              commandHook("printf '%s\\n' Stop >> " + trace),
			})
			session := fix.startTUI(engineClaude, nil, nil)
			session.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
			session.typeLine("inspect atlas")
			session.waitFrame(
				"permission prompt",
				func(s string) bool { return strings.Contains(s, "Allow") && strings.Contains(s, "Deny") },
			)
			assertClaudeGolden(
				t,
				notification,
				"Notification-subagent-permission",
				fix.work,
				fix.claudeTranscript(fixtureSession),
			)
			if approved {
				session.typeRaw("\r")
				session.waitFrame(
					"approved reply",
					func(s string) bool { return strings.Contains(s, "permission answered") },
				)
			} else {
				session.typeRaw("\x1b")
				session.waitFrame("denied turn", func(s string) bool { return strings.Contains(s, "Interrupted") })
			}
			expectedTrace := "PermissionRequest\n"
			if approved {
				expectedTrace += "PostToolUse\nStop\n"
			}
			if actual := readFile(t, trace); actual != expectedTrace {
				t.Fatalf("%s: hook trace=%q, want %q", trace, actual, expectedTrace)
			}
			wantBytes := "unchanged\n"
			if approved {
				wantBytes = "approved report\n"
			}
			if got := readFile(t, filepath.Join(fix.work, "report.txt")); got != wantBytes {
				t.Fatalf("report bytes=%q want=%q", got, wantBytes)
			}
			session.typeLine("/exit")
			if code := session.waitExit(); code != 0 {
				t.Fatalf("exit=%d, want 0", code)
			}
		})
	}
}

func TestClaudeQuestionAnswerUnblocksBeforeHeldTurn(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.root, "hold")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(fix.root, "question-events")
	answer := filepath.Join(fix.root, "answer.json")
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), map[string]any{
		hookPreToolUse:   commandHook(`printf 'PreToolUse\n' >> "` + trace + `"`),
		hookNotification: commandHook(`printf 'Notification\n' >> "` + trace + `"`),
		hookPostToolUse:  commandHook(`cat > "` + answer + `"; printf 'PostToolUse\n' >> "` + trace + `"`),
		hookStop:         commandHook(`printf 'Stop\n' >> "` + trace + `"`),
	})
	fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{
		{Type: StepQuestion, Message: "Pick a target", Options: []string{"atlas", "beacon"}},
		{Type: StepHold, UntilGone: gate},
		{Type: StepTurn, Reply: "answered"},
	}})
	session := fix.startTUI(engineClaude, nil, nil)
	session.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
	session.typeLine("choose")
	session.waitFrame("question", func(s string) bool { return strings.Contains(s, "beacon") })
	session.typeRaw("2\r")
	session.waitFrame("working after answer", func(s string) bool { return strings.Contains(s, "esc to interrupt") })
	if got := readFile(t, trace); got != "PreToolUse\nNotification\nPostToolUse\n" {
		t.Fatalf("%s: events=%q want pre, notification, post before hold", trace, got)
	}
	var payload struct {
		ToolName string `json:"tool_name"`
		Response struct {
			Answers map[string]string `json:"answers"`
		} `json:"tool_response"`
	}
	if err := json.Unmarshal([]byte(readFile(t, answer)), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ToolName != "AskUserQuestion" || payload.Response.Answers["Pick a target"] != "beacon" {
		t.Fatalf("%s: answer=%+v want AskUserQuestion beacon", answer, payload)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	session.waitFrame("finished answer", func(s string) bool { return strings.Contains(s, "⏺ answered") })
	if got := readFile(t, trace); got != "PreToolUse\nNotification\nPostToolUse\nStop\n" {
		t.Fatalf("%s: final events=%q", trace, got)
	}
}
