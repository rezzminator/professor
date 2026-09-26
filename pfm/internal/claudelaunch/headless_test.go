package claudelaunch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderHeadlessPayload(t *testing.T) {
	got, err := RenderHeadless(map[string]any{"theme": "x", "maxSubagentSpawnDepth": 3})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["outputStyle"] != "default" || payload["theme"] != "x" {
		t.Errorf("payload=%#v", payload)
	}
	if payload["env"].(map[string]any)["CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH"] != "3" {
		t.Errorf("payload=%#v", payload)
	}
	empty, err := RenderHeadless(map[string]any{})
	if err != nil || empty != `{"outputStyle":"default"}` {
		t.Errorf("empty=%q err=%v", empty, err)
	}
	for key, value := range map[string]any{"unknown": true, "permissionMode": "bypass", "binary": "claude", "systemPrompt": "professor"} {
		_, err := RenderHeadless(map[string]any{key: value})
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s error=%v", key, err)
		}
	}
}

func TestParseSettings(t *testing.T) {
	got, err := ParseSettings([]byte(`{"claude":{"theme":"x","cache1h":false,"maxSubagentSpawnDepth":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "x" || got["cache1h"] != false || got["maxSubagentSpawnDepth"].(json.Number) != "3" {
		t.Errorf("settings=%#v", got)
	}
	for _, entry := range []struct{ raw, want string }{
		{`{"alien":1}`, "alien"},
		{`{"cache1h":"yes"}`, "cache1h"},
		{`{"theme":"x"} {"theme":"y"}`, "trailing"},
	} {
		_, err := ParseSettings([]byte(entry.raw))
		if err == nil || !strings.Contains(err.Error(), entry.want) {
			t.Errorf("raw=%q error=%v", entry.raw, err)
		}
	}
}
