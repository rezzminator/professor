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
