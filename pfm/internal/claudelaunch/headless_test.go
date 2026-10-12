package claudelaunch

import (
	"encoding/json"
	"reflect"
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
	empty, err := RenderHeadless(nil)
	if err != nil ||
		empty != `{"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-haiku-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1"},"outputStyle":"default"}` {
		t.Errorf("empty=%q err=%v", empty, err)
	}
	window, err := RenderHeadless(map[string]any{"autoCompactWindow": 250000})
	if err != nil ||
		window != `{"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-haiku-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"250000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1"},"outputStyle":"default"}` {
		t.Errorf("window=%q err=%v", window, err)
	}
	helper, err := RenderHeadless(map[string]any{"helperModel": "claude-sonnet-5-5"})
	if err != nil ||
		helper != `{"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-sonnet-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1"},"outputStyle":"default"}` {
		t.Errorf("helper=%q err=%v", helper, err)
	}
	for _, value := range []any{"", "  ", "claude haiku", 5, true} {
		_, err := RenderHeadless(map[string]any{"helperModel": value})
		if err == nil || !strings.Contains(err.Error(), "helperModel") {
			t.Errorf("helperModel %#v error=%v", value, err)
		}
	}
	for key, value := range map[string]any{"unknown": true, "permissionMode": "bypass", "binary": "claude", "systemPrompt": "professor"} {
		_, err := RenderHeadless(map[string]any{key: value})
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s error=%v", key, err)
		}
	}
}

func TestRenderHeadlessCacheLifetimeMainChatOnly(t *testing.T) {
	for _, entry := range []struct {
		settings map[string]any
		want     map[string]string
	}{
		{
			map[string]any{"cache1h": true},
			map[string]string{"CACHE_LIVE_CONTROL_MAIN_TTL": "1h"},
		},
		{
			map[string]any{"cache1h": false},
			map[string]string{"CACHE_LIVE_CONTROL_MAIN_TTL": "5m"},
		},
		// No cache key passed: nothing is set, and the run scrubs the inherited
		// global switches, so Claude falls back to 5m for main and sub-agents.
		{nil, map[string]string{}},
	} {
		got, err := RenderHeadless(entry.settings)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct{ Env map[string]string }
		if err := json.Unmarshal([]byte(got), &payload); err != nil {
			t.Fatal(err)
		}
		if env := cacheEnv(payload.Env); !reflect.DeepEqual(env, entry.want) {
			t.Errorf("settings=%v: cache env=%#v, want %#v", entry.settings, env, entry.want)
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
	window, err := ParseSettings([]byte(`{"autoCompactWindow":250000}`))
	if err != nil {
		t.Fatal(err)
	}
	if window["autoCompactWindow"].(json.Number) != "250000" {
		t.Errorf("window settings=%#v err=%v", window, err)
	}
	helper, err := ParseSettings([]byte(`{"claude":{"helperModel":"claude-sonnet-5-5"}}`))
	if err != nil || helper["helperModel"] != "claude-sonnet-5-5" {
		t.Errorf("helper settings=%#v err=%v", helper, err)
	}
	for _, entry := range []struct{ raw, want string }{
		{`{"helperModel":""}`, "helperModel: expected a non-empty model name"},
		{`{"helperModel":"a b"}`, "helperModel"},
		{`{"helperModel":4}`, "helperModel"},
		{`{"alien":1}`, "alien"},
		{`{"cache1h":"yes"}`, "cache1h"},
		{`{"autoCompactWindow":0}`, "autoCompactWindow"},
		{`{"autoCompactWindow":"x"}`, "autoCompactWindow"},
		{`{"theme":"x"} {"theme":"y"}`, "trailing"},
	} {
		_, err := ParseSettings([]byte(entry.raw))
		if err == nil || !strings.Contains(err.Error(), entry.want) {
			t.Errorf("raw=%q error=%v", entry.raw, err)
		}
	}
}
