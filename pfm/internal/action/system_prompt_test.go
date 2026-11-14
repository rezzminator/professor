package action

import (
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func TestProfessorPromptPathUsesRecordedClone(t *testing.T) {
	home := t.TempDir()
	if _, err := ProfessorPromptPath(
		home,
	); err == nil ||
		!strings.Contains(err.Error(), "no source repository recorded") {
		t.Fatalf("missing marker error = %v", err)
	}
	repo := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	got, err := ProfessorPromptPath(home)
	want := filepath.Join(repo, "pfm", "harness-prompts", "generated", "claude.md")
	if err != nil || got != want {
		t.Fatalf("prompt path = %q, %v; want %q", got, err, want)
	}
}

func TestLauncherRunSystemPromptModes(t *testing.T) {
	home := t.TempDir()
	stageProfessorPrompt(t, home)
	professorFile := mustProfessorPromptPath(t, home)
	cases := []struct {
		mode     string
		wantLean bool
		wantFile bool
	}{
		{mode: "", wantLean: false, wantFile: false},
		{mode: pfmconfig.SystemPromptProduction, wantLean: false, wantFile: false},
		{mode: pfmconfig.SystemPromptLean, wantLean: true, wantFile: false},
		{mode: pfmconfig.SystemPromptProfessor, wantLean: false, wantFile: true},
	}
	for _, testCase := range cases {
		run, err := LauncherRun(
			"/bin/claude",
			[]string{"--resume", "abc"},
			"",
			home,
			pfmconfig.Config{}, pfmconfig.ClaudePrefs{SystemPrompt: testCase.mode}, "",
		)
		if err != nil {
			t.Fatalf("LauncherRun(mode=%q) error = %v", testCase.mode, err)
		}
		parsed := parsedShell(t, run)
		if got := parsed.SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] == "1"; got != testCase.wantLean {
			t.Fatalf("mode %q: lean settings present=%v, want %v", testCase.mode, got, testCase.wantLean)
		}
		if got := parsed.PromptFile == professorFile; got != testCase.wantFile {
			t.Fatalf("mode %q: professor flag present=%v, want %v", testCase.mode, got, testCase.wantFile)
		}
	}
}

func TestLauncherRunHygieneStripsInheritedArm(t *testing.T) {
	run, err := LauncherRun("/bin/claude", nil, "", "/home/test", pfmconfig.Config{}, pfmconfig.ClaudePrefs{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "env -u CLAUDE_CODE_SESSION_ID -u CLAUDECODE -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CONFIG_DIR -u PFM_CLAUDE_CONFIG_DIR_DEFAULT -u CLAUDE_PROJECT_DIR -u ENABLE_PROMPT_CACHING_1H -u FORCE_PROMPT_CACHING_5M -u CLAUDE_CODE_PROMPT_CACHE_TTL -u CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL -u CACHE_LIVE_CONTROL_MAIN_TTL -u CACHE_LIVE_CONTROL_AGENTS_TTL -u CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT -u ANTHROPIC_BASE_URL -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_API_KEY -u ANTHROPIC_MODEL -u ANTHROPIC_SMALL_FAST_MODEL -u CLAUDE_CODE_AUTO_COMPACT_WINDOW -u CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC -u CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK -u CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY -u CODEX_THREAD_ID CACHE_LIVE_CONTROL_MAIN_TTL='5m' PFM_COST_LEDGER='off' '/bin/claude' '--session-id' '{uuid}' '--settings' '{\"cleanupPeriodDays\":0,\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"0\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS\":\"0\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"8\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"0\"},\"hooks\":{\"Notification\":[{\"hooks\":[{\"async\":true,\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude notification\",\"type\":\"command\"}],\"matcher\":\"permission_prompt|elicitation_dialog|idle_prompt|plugin_notification\"}],\"PermissionRequest\":[{\"hooks\":[{\"async\":true,\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude blocked\",\"type\":\"command\"}]}],\"PostToolUse\":[{\"hooks\":[{\"async\":true,\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude working\",\"type\":\"command\"}]}],\"PostToolUseFailure\":[{\"hooks\":[{\"async\":true,\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude working\",\"type\":\"command\"}]}],\"PreToolUse\":[{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal git-guard\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal store-guard\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal orchestrator-wait\",\"type\":\"command\"}],\"matcher\":\"Bash\"},{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal pcm-guard\",\"type\":\"command\"}],\"matcher\":\"Edit|Write|MultiEdit|NotebookEdit|Bash\"}],\"SessionEnd\":[{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal clear-kill\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal exit-close\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude end\",\"type\":\"command\"}]}],\"SessionStart\":[{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal launcher-repair\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude start\",\"type\":\"command\"}]},{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal resume-unkill\",\"type\":\"command\"}],\"matcher\":\"resume\"},{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal clear-name\",\"type\":\"command\"}],\"matcher\":\"clear\"}],\"Stop\":[{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude idle\",\"type\":\"command\"}]}],\"SubagentStart\":[{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal rr-dir\",\"type\":\"command\"}],\"matcher\":\"rr|rr-pro|rr-pro-max\"},{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude agent-start\",\"type\":\"command\"}]}],\"SubagentStop\":[{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude agent-stop\",\"type\":\"command\"}]}],\"UserPromptSubmit\":[{\"hooks\":[{\"command\":\"/home/test/.local/bin/pfm usage-hook\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal clear-name\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal epic-inject\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal reload-intercept\",\"type\":\"command\"},{\"command\":\"/home/test/.local/bin/pfm internal exit-intercept\",\"type\":\"command\"},{\"async\":true,\"command\":\"/home/test/.local/bin/pfm internal herdr-state --engine claude working\",\"type\":\"command\"}]}]},\"outputStyle\":\"default\",\"statusLine\":{\"command\":\"/home/test/.local/bin/pfm-statusline\",\"hideVimModeIndicator\":true,\"padding\":0,\"refreshInterval\":2,\"type\":\"command\"}}'"; uuidPattern.ReplaceAllString(
		run,
		"{uuid}",
	) != want {
		t.Errorf("launcher run = %q, want %q", run, want)
	}
}

func TestClaudeCommandSystemPromptModes(t *testing.T) {
	home := t.TempDir()
	stageProfessorPrompt(t, home)
	for _, testCase := range []struct {
		mode     string
		wantLean bool
		wantFile bool
	}{
		{mode: pfmconfig.SystemPromptProduction},
		{mode: pfmconfig.SystemPromptLean, wantLean: true},
		{mode: pfmconfig.SystemPromptProfessor, wantFile: true},
	} {
		machine := testMachineConfig(home)
		machine.Claude.SystemPrompt = testCase.mode
		run, err := claudeCommandWith(PurposeResume, hygieneNames, home, 1, false, machine, "", "--resume", "abc")
		if err != nil {
			t.Fatalf("mode %q: claudeCommandWith error = %v", testCase.mode, err)
		}
		parsed := parsedShell(t, run)
		if got := parsed.SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] == "1"; got != testCase.wantLean {
			t.Fatalf("mode %q: lean settings present=%v, want %v", testCase.mode, got, testCase.wantLean)
		}
		if got := parsed.PromptFile == mustProfessorPromptPath(t, home); got != testCase.wantFile {
			t.Fatalf("mode %q: professor flag present=%v, want %v", testCase.mode, got, testCase.wantFile)
		}
	}
}

// The managed launcher states a professor-mode chat started without the fleet
// prompt, with the cause; a staged prompt and a caller that states its own
// prompt file are clean. The launcher's policy reaching it is
// hookentry.TestLaunchWarnsWhenTheFleetPromptCannotReachTheChat's.
func TestLauncherLaunchAsPromptWarning(t *testing.T) {
	for _, entry := range []struct {
		name  string
		mode  string
		stage bool
		args  []string
		want  string
	}{
		{"professor, no clone recorded", pfmconfig.SystemPromptProfessor, false, nil, "Claude started WITHOUT its system prompt file: the composed fleet prompt cannot be located: resolve generated harness prompt: read source repository marker {root}/001/.local/share/pfm/install/source-repo: no source repository recorded: open {root}/001/.local/share/pfm/install/source-repo: no such file or directory — fix the cause, then reload the chat"},
		{"professor, prompt staged", pfmconfig.SystemPromptProfessor, true, nil, ""},
		{
			"professor, caller states its own file", pfmconfig.SystemPromptProfessor, false,
			[]string{"--system-prompt-file", "/elsewhere.md"},
			"",
		},
	} {
		t.Run(entry.name, func(t *testing.T) {
			home := t.TempDir()
			if entry.stage {
				stageProfessorPrompt(t, home)
			}
			_, got, err := LauncherLaunchAs(
				workbench.Persona{}, "/usr/bin/claude", entry.args, "", home, pfmconfig.Config{},
				pfmconfig.ClaudePrefs{SystemPrompt: entry.mode}, "",
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := rooted(t, got); got != entry.want {
				t.Errorf("LauncherLaunchAs warning = %q, want %q", got, entry.want)
			}
		})
	}
}
