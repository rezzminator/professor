package mockengine

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAgentRunsItsOwnToolAndStopsBeforeParent(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	order := filepath.Join(fix.recordDir, "order")
	hooks := map[string]any{}
	for _, event := range []string{hookPreToolUse, hookPostToolUse, hookSubagentStart, hookSubagentStop, hookStop} {
		hooks[event] = commandHook(
			`printf '` + event + `\n' >> '` + order + `'; cat >> '` + filepath.Join(
				fix.recordDir,
				event+".jsonl",
			) + `'`,
		)
	}
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), hooks)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	receipt := filepath.Join(fix.work, "child-receipt")
	childPrompt := inlineMarker + string(mustJSON(t, []Step{
		{
			Type:  StepToolCall,
			Tool:  "Bash",
			Input: mustJSON(t, map[string]string{"command": "printf child > '" + receipt + "'"}),
		},
		{Type: StepTurn, Reply: "child finished"},
	}))
	prompt := inlineMarker + string(mustJSON(t, []Step{
		{
			Type: StepToolCall,
			Tool: "Agent",
			Input: mustJSON(
				t,
				map[string]string{"description": "inspect", "subagent_type": "general-purpose", "prompt": childPrompt},
			),
		},
		{Type: StepTurn, Reply: "parent finished"},
	}))
	code, _, stderr := runOnce(fix, engineClaude, claudeArgs("-p", prompt), "")
	if code != 0 {
		t.Fatalf("Agent exit=%d stderr=%q, want 0", code, stderr)
	}
	if got := readFile(t, receipt); got != "child" {
		t.Fatalf("%s=%q, want child", receipt, got)
	}
	want := "PreToolUse\nSubagentStart\nPreToolUse\nPostToolUse\nSubagentStop\nPostToolUse\nStop\n"
	if got := readFile(t, order); got != want {
		t.Fatalf("%s order=%q, want %q", order, got, want)
	}
	var start, stop hookPayload
	if err := json.Unmarshal([]byte(fix.recorded(hookSubagentStart)[0]), &start); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fix.recorded(hookSubagentStop)[0]), &stop); err != nil {
		t.Fatal(err)
	}
	if start.AgentID == "" || stop.AgentID != start.AgentID || stop.LastAssistantMessage != "child finished" ||
		stop.AgentType != "general-purpose" {
		t.Fatalf("Agent start=%+v stop=%+v; want matching real sidechain identity and child finished", start, stop)
	}
	child := readFile(t, stop.AgentTranscriptPath)
	var records []claudeRecord
	for _, line := range strings.Split(strings.TrimSpace(child), "\n") {
		var record claudeRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.AgentID != start.AgentID || !record.IsSidechain {
			t.Fatalf("%s child record=%s lacks own identity", stop.AgentTranscriptPath, line)
		}
		records = append(records, record)
	}
	if got := records[len(records)-1].Message.StopReason; got != "end_turn" {
		t.Fatalf("%s stop_reason=%q, want end_turn", stop.AgentTranscriptPath, got)
	}
}
