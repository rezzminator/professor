package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClaudeSiblingEventsKeepPermissionChooserAndPendingTool(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	before, during := filepath.Join(fix.root, "before-permission"), filepath.Join(fix.root, "during-permission")
	for _, path := range []string{before, during} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	trace := filepath.Join(fix.root, "events.jsonl")
	hooks := map[string]any{}
	for _, event := range []string{hookPreToolUse, hookPermissionRequest, hookNotification, hookPostToolUse, hookPostToolUseFailure, hookStop, hookSubagentStop} {
		hooks[event] = commandHook(`cat >> "` + trace + `"; printf '\n' >> "` + trace + `"`)
	}
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), hooks)
	rawEvents := []Step{
		attentionGoldenStep(
			t,
			"PostToolUse",
			map[string]any{
				"tool_name":   "Write",
				"tool_use_id": "toolu_sibling",
				"tool_input":  map[string]any{"file_path": "sibling.txt", "content": "sibling bytes"},
				"agent_id":    "agent-sibling",
			},
		),
		attentionGoldenStep(
			t,
			"PostToolUseFailure",
			map[string]any{
				"tool_name":    "Bash",
				"tool_use_id":  "toolu_interrupted",
				"is_interrupt": true,
				"error":        "sibling interrupted",
			},
		),
		attentionGoldenStep(t, "Stop", nil),
		attentionGoldenStep(t, "SubagentStop", nil),
	}
	concurrent := append([]Step{{Type: StepHold, UntilGone: during}}, rawEvents...)
	concurrent = append(
		concurrent,
		Step{Type: StepNotification, NotificationType: "permission_prompt", Message: "sibling sequence complete"},
	)
	fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{
		{Type: StepHold, UntilGone: before},
		{
			Type:   StepPermission,
			Tool:   "Write",
			Input:  json.RawMessage(`{"file_path":"report.txt","content":"approved\n"}`),
			During: concurrent,
		},
		{Type: StepTurn, Reply: "permission answered"},
	}})
	session := fix.startTUI(engineClaude, nil, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("inspect atlas")
	session.waitFrame(
		"busy before chooser",
		func(frame string) bool { return strings.Contains(frame, "esc to interrupt") },
	)
	session.typeRaw("pending draft")
	session.waitFrame(
		"draft before chooser",
		func(frame string) bool { return strings.Contains(frame, "❯ pending draft") },
	)
	if err := os.Remove(before); err != nil {
		t.Fatal(err)
	}
	session.waitFrame(
		"permission chooser",
		func(frame string) bool { return strings.Contains(frame, "Enter to confirm · Esc to cancel") },
	)
	session.typeRaw("2")
	frameBefore := session.waitFrame(
		"second choice selected",
		func(frame string) bool { return strings.Contains(frame, "❯ 2. Deny") },
	)
	if err := os.Remove(during); err != nil {
		t.Fatal(err)
	}
	events := waitAttentionReceipts(t, trace, 8)
	if got := session.out.frame(); got != frameBefore {
		t.Fatalf("chooser after sibling events=%q, want unchanged %q", got, frameBefore)
	}
	for index := range rawEvents {
		var want map[string]any
		if err := json.Unmarshal(rawEvents[index].Payload, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(events[index+3], want) {
			t.Fatalf("%s: sibling event %d=%v, want %v", trace, index, events[index+3], want)
		}
	}
	if events[7]["message"] != "sibling sequence complete" {
		t.Fatalf("%s: final concurrent event=%v", trace, events[7])
	}
	session.typeRaw("1\r")
	frameAfter := session.waitFrame(
		"answered turn",
		func(frame string) bool { return strings.Contains(frame, "⏺ permission answered") },
	)
	events = waitAttentionReceipts(t, trace, 10)
	post := events[8]
	if post["hook_event_name"] != hookPostToolUse || post["tool_name"] != "Write" ||
		post["tool_use_id"] != events[0]["tool_use_id"] ||
		!reflect.DeepEqual(post["tool_input"], map[string]any{"file_path": "report.txt", "content": "approved\n"}) ||
		events[9]["hook_event_name"] != hookStop {
		t.Fatalf(
			"%s: original pre=%v post=%v stop=%v, want original Write completed once",
			trace,
			events[0],
			post,
			events[9],
		)
	}
	if got := readFile(t, filepath.Join(fix.work, "report.txt")); got != "approved\n" {
		t.Fatalf("approved bytes=%q, want approved\\n", got)
	}
	draft := ""
	for _, line := range strings.Split(frameAfter, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "❯ ") {
			draft = line
		}
	}
	if draft != "❯ pending draft" {
		t.Fatalf("composer after chooser=%q, want preserved pending draft; frame=%q", draft, frameAfter)
	}
}

func TestClaudeAnswerCancelsPendingSiblingEvents(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.root, "during-permission")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(fix.root, "events.jsonl")
	hooks := map[string]any{}
	for _, event := range []string{hookPostToolUse, hookStop, hookSubagentStop} {
		hooks[event] = commandHook(`cat >> "` + trace + `"; printf '\n' >> "` + trace + `"`)
	}
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), hooks)
	fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{
		{
			Type:   StepPermission,
			Tool:   "Write",
			Input:  json.RawMessage(`{"file_path":"report.txt","content":"approved\n"}`),
			During: []Step{{Type: StepHold, UntilGone: gate}, attentionGoldenStep(t, "SubagentStop", nil)},
		},
		{Type: StepTurn, Reply: "permission answered"},
	}})
	session := fix.startTUI(engineClaude, nil, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("inspect atlas")
	session.waitFrame(
		"permission chooser",
		func(frame string) bool { return strings.Contains(frame, "Enter to confirm · Esc to cancel") },
	)
	session.typeRaw("\r")
	session.waitFrame(
		"answered turn",
		func(frame string) bool { return strings.Contains(frame, "⏺ permission answered") },
	)
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	events := waitAttentionReceipts(t, trace, 2)
	if events[0]["hook_event_name"] != hookPostToolUse || events[0]["tool_name"] != "Write" ||
		events[1]["hook_event_name"] != hookStop {
		t.Fatalf("%s: events=%v, want original Write PostToolUse then Stop", trace, events)
	}
}

func TestClaudeConcurrentEventFailureEndsTheMockByName(t *testing.T) {
	fix := newFixture(t)
	fix.write(
		Scenario{
			Steps: []Step{
				{
					Type:  StepPermission,
					Tool:  "Write",
					Input: json.RawMessage(`{"file_path":"report.txt","content":"approved\n"}`),
					During: []Step{
						{
							Type:    StepHook,
							Event:   hookStop,
							Payload: json.RawMessage(`{"hook_event_name":"PostToolUse"}`),
						},
					},
				},
			},
		},
	)
	session := fix.startTUI(engineClaude, nil, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("inspect atlas")
	if code := session.waitExit(); code != ExitUnpinned {
		t.Fatalf("exit=%d, want %d", code, ExitUnpinned)
	}
	want := "mock-engine: attention: during step 0: scripted hook_event_name must equal Stop\n"
	if got := session.stderr.String(); got != want {
		t.Fatalf("stderr=%q, want %q", got, want)
	}
}

func attentionGoldenStep(t *testing.T, name string, overrides map[string]any) Step {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate attention hook goldens")
	}
	path := filepath.Join(filepath.Dir(source), "testdata", "hooks", "claude", name+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for key, value := range overrides {
		payload[key] = value
	}
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := payload["tool_name"].(string)
	return Step{Type: StepHook, Event: name, Tool: tool, Payload: raw}
}

func waitAttentionReceipts(t *testing.T, path string, count int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var events []map[string]any
	var parseErr error
	for {
		raw, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("%s: read attention events: %v", path, err)
		}
		events = nil
		parseErr = nil
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			var event map[string]any
			if parseErr = json.Unmarshal([]byte(line), &event); parseErr != nil {
				break
			}
			events = append(events, event)
		}
		if parseErr == nil && len(events) == count {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: attention events=%v error=%v, want %d complete receipts", path, events, parseErr, count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
