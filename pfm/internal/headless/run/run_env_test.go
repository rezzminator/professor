package run

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestClaudeEnvironmentUsesRegistryHygiene(t *testing.T) {
	for _, test := range []struct {
		name     string
		explicit bool
		wantKey  bool
	}{
		{"inherited", false, false},
		{"explicit", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got []string
			setEnvironment([]string{
				"ENABLE_PROMPT_CACHING_1H=1", "ANTHROPIC_API_KEY=key", "CLAUDE_CODE_SESSION_ID=parent",
				"CLAUDE_PROJECT_DIR=/parent", "TMUX=parent", "TMUX_PANE=parent",
				"OPENAI_API_KEY=openai", "OPENAI_BASE_URL=https://endpoint",
			}, pfmengine.Claude, "/cfg", test.explicit, &got)
			for _, name := range []string{"CLAUDE_CODE_SESSION_ID", "TMUX", "TMUX_PANE"} {
				if lastEnvironmentValue(got, name) != "" {
					t.Fatalf("%s leaked: %q", name, got)
				}
			}
			for _, name := range []string{"ENABLE_PROMPT_CACHING_1H", "ANTHROPIC_API_KEY", "CLAUDE_PROJECT_DIR", "OPENAI_API_KEY", "OPENAI_BASE_URL"} {
				if (lastEnvironmentValue(got, name) != "") != test.wantKey {
					t.Fatalf("%s in %q, explicit=%t", name, got, test.explicit)
				}
			}
		})
	}
}

func TestClaudeEnvironmentDropsInheritedLaunchSettings(t *testing.T) {
	var got []string
	setEnvironment([]string{
		"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH=9",
		"CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS=32",
		"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION=99",
		"CLAUDE_CODE_TMUX_TRUECOLOR=1",
		"CLAUDE_CODE_NATIVE_CURSOR=1",
	}, pfmengine.Claude, "/cfg", false, &got)
	for _, entry := range got {
		if strings.HasPrefix(entry, "CLAUDE_CODE_MAX_") ||
			strings.HasPrefix(entry, "CLAUDE_CODE_TMUX_TRUECOLOR=") ||
			strings.HasPrefix(entry, "CLAUDE_CODE_NATIVE_CURSOR=") {
			t.Fatalf("inherited launch setting reached Claude: %q", got)
		}
	}
}

func TestClaudeEnvironmentDoesNotAppendDescriptorLaunchEnv(t *testing.T) {
	var got []string
	setEnvironment([]string{"PATH=/usr/bin"}, pfmengine.Claude, "/cfg", false, &got)
	if lastEnvironmentValue(got, "CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION") != "" {
		t.Fatalf("descriptor launch environment leaked: %q", got)
	}
}

func TestOtherEngineEnvironmentKeepsClaudeProjectDir(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Codex, pfmengine.OpenCode} {
		var got []string
		setEnvironment([]string{"CLAUDE_PROJECT_DIR=/parent"}, engine, "", false, &got)
		if value := lastEnvironmentValue(got, "CLAUDE_PROJECT_DIR"); value != "/parent" {
			t.Fatalf("%s CLAUDE_PROJECT_DIR = %q, want /parent", engine, value)
		}
	}
}

func TestClaudeResolveDoesNotUseAskModelOrEffort(t *testing.T) {
	binary := writeEngineStub(t, "exit 0")
	machine := claudeMachine(binary, t.TempDir())
	machine.Ask.Prefs = map[pfmengine.ID]pfmconfig.EnginePrefs{
		pfmengine.Claude: {Model: "ask-model", Effort: "high"},
	}
	request, err := Resolve(Request{Config: machine, Engine: pfmengine.Claude})
	if err != nil {
		t.Fatal(err)
	}
	if request.Model != "" || request.Effort != "" {
		t.Fatalf("Claude resolved model=%q effort=%q from ask block", request.Model, request.Effort)
	}
}

func TestResolveUnknownClaudeAccountNamesRosterError(t *testing.T) {
	machine := claudeMachine(writeEngineStub(t, "exit 0"), t.TempDir())
	_, err := Resolve(Request{Config: machine, Engine: pfmengine.Claude, Account: 99})
	if err == nil || !strings.Contains(err.Error(), "account 99 is not in the configured roster") {
		t.Fatalf("Resolve unknown account error = %v", err)
	}
}

func TestResolveChecksOnlyClaudeConfigDir(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode} {
		for _, withoutAccount := range []bool{false, true} {
			name := string(engine) + "/roster"
			if withoutAccount {
				name = string(engine) + "/without-account"
			}
			t.Run(name, func(t *testing.T) {
				binary := writeEngineStub(t, "exit 0")
				dir := filepath.Join(t.TempDir(), string(engine))
				if engine == pfmengine.OpenCode {
					dir = filepath.Join(filepath.Dir(dir), "opencode")
				}
				machine := pfmconfig.Config{
					Claude:   pfmconfig.ClaudePrefs{Binary: binary},
					Codex:    pfmconfig.CodexPrefs{Binary: binary},
					OpenCode: pfmconfig.OpenCodePrefs{Binary: binary},
				}
				request := Request{Config: machine, Engine: engine, Native: true, Model: "test-model"}
				if withoutAccount {
					request.WithoutAccount = true
					name := pfmengine.MustLookup(engine).HomeEnv
					if engine == pfmengine.OpenCode {
						request.Env = []string{"XDG_DATA_HOME=" + filepath.Dir(dir)}
					} else {
						request.Env = []string{name + "=" + dir}
					}
				} else {
					request.Config.Accounts = []pfmconfig.Account{{ID: 1, ConfigDir: dir}}
					request.Config.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: dir}}
					request.Config.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 1, Home: dir}}
				}
				resolved, err := Resolve(request)
				if engine == pfmengine.Claude {
					want := dir + " does not exist — run pfm install"
					if !withoutAccount {
						want = "account 1: " + want
					}
					if err == nil || err.Error() != want {
						t.Fatalf("Resolve error=%v, want %q", err, want)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if resolved.ConfigDir != dir {
					t.Fatalf("ConfigDir=%q, want %q", resolved.ConfigDir, dir)
				}
			})
		}
	}
}

// TestClaudeEnvironmentDropsTheLoginDefaultSentinel: a headless run sets its
// own CLAUDE_CONFIG_DIR, so the login default's sentinel must not ride along
// and make the child's readers take pfm's choice for the login default.
func TestClaudeEnvironmentDropsTheLoginDefaultSentinel(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		var got []string
		setEnvironment([]string{
			"CLAUDE_CONFIG_DIR=/home/test/.cc/1", claudelaunch.ConfigDirDefaultEnv + "=/home/test/.cc/1",
		}, pfmengine.Claude, "/cfg2", explicit, &got)
		if value := lastEnvironmentValue(got, claudelaunch.ConfigDirDefaultEnv); value != "" {
			t.Errorf("explicit=%t: sentinel survived as %q in %q", explicit, value, got)
		}
		if value := lastEnvironmentValue(got, "CLAUDE_CONFIG_DIR"); value != "/cfg2" {
			t.Errorf("explicit=%t: CLAUDE_CONFIG_DIR=%q, want /cfg2", explicit, value)
		}
	}
}
