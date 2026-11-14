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

func TestRunEnvironment(t *testing.T) {
	launch := Launch{Unset: []string{"CLAUDECODE"}, Env: WithEnvs(
		map[string]string{"TEAM_MODE": "strict", "API_REGION": "eu", "CLAUDE_CONFIG_DIR": "/adopter/store"},
		[]string{"CLAUDE_CONFIG_DIR=/store/2", "CACHE_LIVE_CONTROL_MAIN_TTL=1h"},
	)}
	for _, test := range []struct {
		name    string
		environ []string
		want    []string
	}{
		{
			name:    "inherited copies of assigned names go",
			environ: []string{"PATH=/bin", "CLAUDECODE=1", "TEAM_MODE=loose", "CLAUDE_CONFIG_DIR=/store/1"},
			want: []string{
				"PATH=/bin", "API_REGION=eu", "TEAM_MODE=strict",
				"CLAUDE_CONFIG_DIR=/store/2", "CACHE_LIVE_CONTROL_MAIN_TTL=1h",
			},
		},
		{
			name:    "no settings keeps the launch's own assignments",
			environ: []string{"PATH=/bin"},
			want:    []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/store/2"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := launch
			if test.name == "no settings keeps the launch's own assignments" {
				current.Env = WithEnvs(nil, []string{"CLAUDE_CONFIG_DIR=/store/2"})
			}
			if got := RunEnvironment(test.environ, current); !reflect.DeepEqual(got, test.want) {
				t.Errorf("RunEnvironment = %q, want %q", got, test.want)
			}
		})
	}
	if got := WithEnvs(nil, nil); got != nil {
		t.Errorf("WithEnvs(nil, nil) = %#v, want nil", got)
	}
}
