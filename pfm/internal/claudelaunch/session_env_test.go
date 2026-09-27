package claudelaunch

import (
	"reflect"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestSessionEnv(t *testing.T) {
	got := SessionEnv(pfmconfig.ClaudePrefs{AutoCompactWindow: 50000})
	want := []string{
		"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW=50000",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionEnv() = %q, want %q", got, want)
	}
}
