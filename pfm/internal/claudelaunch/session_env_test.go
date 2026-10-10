package claudelaunch

import (
	"reflect"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestSessionEnv(t *testing.T) {
	for _, entry := range []struct {
		prefs pfmconfig.ClaudePrefs
		want  []string
	}{
		{
			pfmconfig.ClaudePrefs{AutoCompactWindow: 50000, HelperModel: "claude-sonnet-5-5"},
			[]string{
				"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW=50000",
				"ANTHROPIC_SMALL_FAST_MODEL=claude-sonnet-5-5",
			},
		},
		// An unset helper model is Haiku 5.5, never an empty value Claude Code would read as a model.
		{
			pfmconfig.ClaudePrefs{AutoCompactWindow: 50000},
			[]string{
				"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW=50000",
				"ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-5-5",
			},
		},
	} {
		if got := SessionEnv(entry.prefs); !reflect.DeepEqual(got, entry.want) {
			t.Errorf("SessionEnv(%+v) = %q, want %q", entry.prefs, got, entry.want)
		}
	}
}
