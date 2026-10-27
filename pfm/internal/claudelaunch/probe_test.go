package claudelaunch

import (
	"reflect"
	"strings"
	"testing"
)

func TestProbeEnv(t *testing.T) {
	input := []string{"KEEP=value"}
	for _, name := range Hygiene() {
		input = append(input, name+"=old")
	}
	input = append(input,
		"CLAUDE_CODE_USE_BEDROCK=1",
		"CLAUDE_CODE_USE_VERTEX=1",
		"ANTHROPIC_BEDROCK_BASE_URL=https://bedrock.example",
		"ANTHROPIC_VERTEX_PROJECT_ID=proj",
	)
	want := []string{
		"KEEP=value",
		"ANTHROPIC_BASE_URL=http://sink",
		"ANTHROPIC_API_KEY=pfm-doctor-sink",
		"ANTHROPIC_AUTH_TOKEN=pfm-doctor-sink",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=0",
		"FORCE_PROMPT_CACHING_5M=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CONFIG_DIR=/scratch",
	}
	if got := ProbeEnv(input, "http://sink", "/scratch"); !reflect.DeepEqual(got, want) {
		t.Errorf("ProbeEnv=%q, want %q", got, want)
	}
}

// The doctor's prompt capture runs no turn; it is never the chat bound to the
// parent's Herdr pane, so none of the four Herdr names reaches it.
func TestProbeEnvDropsTheHerdrPane(t *testing.T) {
	input := []string{
		"KEEP=value", "HERDR_ENV=1", "HERDR_PANE_ID=w1:p1", "HERDR_SOCKET_PATH=/tmp/h.sock",
		"HERDR_BIN_PATH=/usr/local/bin/herdr",
	}
	for _, entry := range ProbeEnv(input, "http://sink", "/scratch") {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "HERDR_") {
			t.Errorf("ProbeEnv kept %q", entry)
		}
	}
	if got := ProbeEnv(input, "http://sink", "/scratch"); got[0] != "KEEP=value" {
		t.Errorf("ProbeEnv = %q, want KEEP=value first", got)
	}
}
