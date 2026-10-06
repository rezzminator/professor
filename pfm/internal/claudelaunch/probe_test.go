package claudelaunch

import (
	"reflect"
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
		"CLAUDE_CONFIG_DIR=/scratch",
	}
	if got := ProbeEnv(input, "http://sink", "/scratch"); !reflect.DeepEqual(got, want) {
		t.Errorf("ProbeEnv=%q, want %q", got, want)
	}
}
