package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

func TestClaudeCompactPrintsTheReceiptAndMarksTheTranscript(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepTurn, Reply: "first"},
		{Type: StepCompact, PostTokens: 777},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("warm up")
	session.waitFrame("the first reply", func(frame string) bool { return strings.Contains(frame, "first") })
	if pfmcall.CompactionReceipt(session.out.frame()) {
		t.Fatal("a receipt is showing before any compaction")
	}
	session.typeLine("/compact")
	session.waitFrame("the compaction receipt pfmcall.CompactionReceipt matches", pfmcall.CompactionReceipt)
	meta := readMeta(t, fix.claudeTranscript(fixtureSession))
	if !meta.CompactedAfterUsage || meta.PostCompactTokens != 777 || meta.HumanPrompts != 1 {
		t.Fatalf("ReadMeta = %+v, want a compaction after usage with 777 post tokens and 1 human prompt", meta)
	}
}

func TestClaudeBackgroundAgentIsIdleAndExitAsksToStopIt(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepBackgroundAgent, Name: "tracer", Status: "mapping the seams"},
		{Type: StepTurn, Reply: "dispatched"},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("map it")
	frame := session.waitFrame("the reply beside an agent row", func(frame string) bool {
		return strings.Contains(frame, "dispatched") && strings.Contains(frame, "● tracer")
	})
	if pfmcall.IsBusy(frame) {
		t.Fatalf("a background agent must leave the main turn idle:\n%s", frame)
	}
	if pfmcall.SelectorLine(frame) != "" {
		t.Fatalf("the agent row read as a menu: %q", pfmcall.SelectorLine(frame))
	}
	session.typeLine("/exit")
	frame = session.waitFrame("the exit confirmation pfmcall.ExitDialogOpen matches", pfmcall.ExitDialogOpen)
	if !strings.Contains(pfmcall.SelectorLine(frame), "Exit") {
		t.Fatalf("selector = %q, want the Exit row preselected", pfmcall.SelectorLine(frame))
	}
	session.typeRaw("\r")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit code after confirming = %d", code)
	}
	meta := readMeta(t, fix.claudeTranscript(fixtureSession))
	rows := fix.indexedTranscripts()
	if meta.HumanPrompts != 1 || len(rows) != 1 || rows[0].PromptCount != 1 {
		t.Fatalf("sidechain records leaked into the count: meta=%+v rows=%+v", meta, rows)
	}
	for _, approved := range []bool{true, false} {
		t.Run(fmt.Sprintf("permission-after-main-stop-%t", approved), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			trace := filepath.Join(fix.recordDir, "background-order")
			hooks := map[string]any{}
			for _, event := range []string{hookPreToolUse, hookSubagentStart, hookStop, hookPermissionRequest, hookNotification, hookPostToolUse, hookSubagentStop} {
				payloadPath := filepath.Join(fix.recordDir, event+".jsonl")
				hooks[event] = commandHook(
					`printf '` + event + `\n' >> '` + trace + `'; cat >> '` + payloadPath + `'; printf '\n' >> '` + payloadPath + `'`,
				)
			}
			writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), hooks)
			childPrompt := inlineMarker + string(mustJSON(t, []Step{
				{
					Type:  StepPermission,
					Tool:  "Write",
					Input: json.RawMessage(`{"file_path":"background-report.txt","content":"approved child report"}`),
				},
				{Type: StepTurn, Reply: "child complete"},
			}))
			input := mustJSON(
				t,
				map[string]string{"description": "research", "subagent_type": "general-purpose", "prompt": childPrompt},
			)
			fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
				{Type: StepBackgroundAgent, Name: "researcher", Input: input},
				{Type: StepTurn, Reply: "research dispatched"},
			}})
			session := fix.startTUI(engineClaude, nil, nil)
			session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			session.typeLine("research it")
			session.waitFrame(
				"main reply",
				func(frame string) bool { return strings.Contains(frame, "research dispatched") },
			)
			events := strings.Split(strings.TrimSpace(readFile(t, trace)), "\n")
			wantStart := []string{hookPreToolUse, hookSubagentStart, hookStop}
			if len(events) < len(wantStart) || !reflect.DeepEqual(events[:len(wantStart)], wantStart) {
				t.Fatalf("%s: background launch order=%q want prefix=%q", trace, events, wantStart)
			}
			session.waitFrame(
				"child permission",
				func(frame string) bool { return strings.Contains(frame, "Allow") && strings.Contains(frame, "Deny") },
			)
			want := "PreToolUse\nSubagentStart\nStop\nPreToolUse\nPermissionRequest\nNotification\n"
			if got := readFile(t, trace); got != want {
				t.Fatalf("%s: permission order=%q want=%q", trace, got, want)
			}
			var pre, start, permission hookPayload
			for event, destination := range map[string]*hookPayload{hookPreToolUse: &pre, hookSubagentStart: &start, hookPermissionRequest: &permission} {
				if err := json.Unmarshal([]byte(fix.recorded(event)[0]), destination); err != nil {
					t.Fatal(err)
				}
			}
			if pre.ToolName != "Agent" || pre.ToolUseID == "" || string(pre.ToolInput) != string(input) ||
				start.AgentID == "" ||
				permission.AgentID != start.AgentID {
				t.Fatalf("native Agent pre=%+v start=%+v permission=%+v", pre, start, permission)
			}
			var parentToolID string
			for _, line := range strings.Split(strings.TrimSpace(readFile(t, fix.claudeTranscript(fixtureSession))), "\n") {
				var record struct {
					Type    string `json:"type"`
					Message *struct {
						Content json.RawMessage `json:"content"`
					} `json:"message"`
				}
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatal(err)
				}
				if record.Type == roleAssistant && record.Message != nil {
					var parts []contentPart
					if err := json.Unmarshal(record.Message.Content, &parts); err != nil {
						t.Fatal(err)
					}
					for _, part := range parts {
						if part.Name == "Agent" {
							parentToolID = part.ID
						}
					}
				}
			}
			if parentToolID != pre.ToolUseID {
				t.Fatalf("Agent transcript tool_use_id=%q want native PreToolUse id=%q", parentToolID, pre.ToolUseID)
			}
			if approved {
				session.typeRaw("\r")
				want += "PostToolUse\nSubagentStop\n"
			} else {
				session.typeRaw("\x1b")
				want += "SubagentStop\n"
			}
			fix.waitRecorded(hookSubagentStop, 1)
			if got := readFile(t, trace); got != want {
				t.Fatalf("%s: child completion order=%q want=%q", trace, got, want)
			}
			if approved {
				if got := readFile(
					t,
					filepath.Join(fix.work, "background-report.txt"),
				); got != "approved child report" {
					t.Fatalf("child report=%q want approved child report", got)
				}
			} else if _, err := os.Stat(filepath.Join(fix.work, "background-report.txt")); !os.IsNotExist(err) {
				t.Fatalf("denied child report stat=%v want not exist", err)
			}
			session.typeLine("/exit")
			if code := session.waitExit(); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
			}
		})
	}
}

func TestClaudeExitWithoutBackgroundWorkQuitsAtOnce(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if pfmcall.ExitDialogOpen(session.out.all()) {
		t.Fatal("an exit dialog appeared with no background work to stop")
	}
}

func TestClaudeBackgroundHandoffChangesSessionWithinTheSameProcess(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	t.Setenv("CLAUDE_CODE_SESSION_ID", fixtureSession)
	const successor = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	fix.write(Scenario{SessionID: fixtureSession})
	setScenarioField(t, fix, "steps", []map[string]any{
		{"type": "background_handoff", "session_id": successor},
		{"type": "turn", "reply": "handed off"},
		{"type": "turn", "reply": "successor reply"},
	})
	starts := filepath.Join(fix.recordDir, "handoff-starts")
	launch := filepath.Join(fix.recordDir, "handoff-launch")
	command := `cat >> "` + starts + `"; printf '\n' >> "` + starts + `"; printf '%s:%s\n' "$PPID" "$CLAUDE_CODE_SESSION_ID" >> "` + launch + `"`
	settings := inlineHookSettings(t, map[string]any{hookSessionStart: commandHook(command)}, "")
	session := fix.startTUI(engineClaude, []string{"--settings", settings}, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("background this chat")
	session.waitFrame("handoff reply", func(frame string) bool { return strings.Contains(frame, "⏺ handed off") })
	session.typeLine("successor prompt")
	session.waitFrame(
		"successor reply",
		func(frame string) bool { return strings.Contains(frame, "⏺ successor reply") },
	)
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
	}
	var continued map[string]any
	old := fix.claudeTranscript(fixtureSession)
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, old)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["type"] == "continued-in" {
			continued = record
		}
	}
	stamp, _ := continued["timestamp"].(string)
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		t.Fatalf("%s: timestamp=%q: %v", old, stamp, err)
	}
	delete(continued, "timestamp")
	want := map[string]any{"type": "continued-in", "sessionId": fixtureSession, "continuedInSessionId": successor}
	if !reflect.DeepEqual(continued, want) {
		t.Fatalf("%s: record=%v want=%v", old, continued, want)
	}
	if got := readFile(t, launch); got != strings.Repeat(fmt.Sprintf("%d:%s\n", os.Getpid(), fixtureSession), 2) {
		t.Fatalf("%s: launch identity=%q", launch, got)
	}
	lines := strings.Split(strings.TrimSpace(readFile(t, starts)), "\n")
	if len(lines) != 2 {
		t.Fatalf("%s: starts=%q want two", starts, lines)
	}
	for index, id := range []string{fixtureSession, successor} {
		var payload hookPayload
		if err := json.Unmarshal([]byte(lines[index]), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.SessionID != id || payload.TranscriptPath != fix.claudeTranscript(id) {
			t.Fatalf("%s: hook=%+v want session=%s transcript=%s", starts, payload, id, fix.claudeTranscript(id))
		}
	}
	if got := readMeta(t, fix.claudeTranscript(successor)); got.HumanPrompts != 1 {
		t.Fatalf("successor transcript=%+v want one successor prompt", got)
	}
	if got := readMeta(t, old); got.HumanPrompts != 1 {
		t.Fatalf("old transcript=%+v want one original prompt", got)
	}
}
