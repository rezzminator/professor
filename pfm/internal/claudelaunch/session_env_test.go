package claudelaunch

import (
	"reflect"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestSessionEnv(t *testing.T) {
	for _, entry := range []struct {
		claude map[string]any
		want   []string
	}{
		{
			map[string]any{"env": map[string]any{
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "50000", "ANTHROPIC_SMALL_FAST_MODEL": "claude-sonnet-5-5",
			}},
			[]string{
				"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW=50000",
				"ANTHROPIC_SMALL_FAST_MODEL=claude-sonnet-5-5",
			},
		},
		// No claude.settings: the shipped defaults, Haiku 5.5 the helper model.
		{
			nil,
			[]string{
				"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000",
				"ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-5-5",
			},
		},
		// A null leaves the variable out, a shipped default included.
		{
			map[string]any{"env": map[string]any{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": nil}},
			[]string{
				"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
				"ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-5-5",
			},
		},
		// A number where Claude reads a string reaches the environment as its JSON text.
		{
			map[string]any{"env": map[string]any{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": float64(250000)}},
			[]string{
				"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW=250000",
				"ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-5-5",
			},
		},
	} {
		if got := SessionEnv(entry.claude); !reflect.DeepEqual(got, entry.want) {
			t.Errorf("SessionEnv(%v) = %q, want %q", entry.claude, got, entry.want)
		}
	}
	// The launch shell and PATH follow the plugin values: the login shell's
	// folders merged into the session's own PATH, its order kept.
	testwait.ProcessClock(t, clock.NewFake(time.Unix(0, 0)))
	runner := loginRunner("/login/bin/zsh", "/opt/mise/shims", "/usr/bin", "/opt/x")
	runner.ScriptLookPath("bash", "/scripted/bin/bash", nil)
	lookup := envLookup(map[string]string{"PATH": "/usr/bin:/bin", "SHELL": "/login/bin/zsh"})
	want := []string{
		"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000",
		"ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-5-5",
		"CLAUDE_CODE_SHELL=/scripted/bin/bash",
		"PATH=/opt/mise/shims:/usr/bin:/bin:/opt/x",
	}
	if got := sessionEnvThrough(nil, lookup, runner); !reflect.DeepEqual(got, want) {
		t.Errorf("SessionEnv with a login PATH = %q, want %q", got, want)
	}
}

// A pane command keeps the launch's PATH out and runs the login folders through
// /bin/sh after the pane shell's own PATH; with no login folders Env is whole.
func TestLaunchPaneEnv(t *testing.T) {
	env := []string{"A=1", "PATH=/pfm/bin:/shims", "B=2"}
	for _, test := range []struct {
		name      string
		login     []string
		wantEnv   []string
		wantWords string
	}{
		{name: "no login folders", wantEnv: env},
		{
			name:      "login folders",
			login:     []string{"/shims", "/opt/x y"},
			wantEnv:   []string{"A=1", "B=2"},
			wantWords: ` /bin/sh -c 'PATH="${PATH:+$PATH:}$1"; export PATH; shift; exec "$@"' sh '/shims:/opt/x y'`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotEnv, gotWords := Launch{Env: env, LoginPATH: test.login}.PaneEnv()
			if !reflect.DeepEqual(gotEnv, test.wantEnv) || gotWords != test.wantWords {
				t.Fatalf("PaneEnv = (%q, %q), want (%q, %q)", gotEnv, gotWords, test.wantEnv, test.wantWords)
			}
		})
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
