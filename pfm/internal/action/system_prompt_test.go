package action

import (
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestLauncherRunSystemPromptModes(t *testing.T) {
	home := t.TempDir()
	stageProfessorPrompt(t, home)
	professorFile := Quote(ProfessorPromptPath(home))
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
			pfmconfig.ClaudePrefs{SystemPrompt: testCase.mode},
		)
		if err != nil {
			t.Fatalf("LauncherRun(mode=%q) error = %v", testCase.mode, err)
		}
		if got := strings.Contains(run, " CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1"); got != testCase.wantLean {
			t.Fatalf("mode %q: lean env present=%v, want %v in %q", testCase.mode, got, testCase.wantLean, run)
		}
		if got := strings.Contains(run, " --system-prompt-file "+professorFile); got != testCase.wantFile {
			t.Fatalf("mode %q: professor flag present=%v, want %v in %q", testCase.mode, got, testCase.wantFile, run)
		}
	}
}

func TestLauncherRunHygieneStripsInheritedArm(t *testing.T) {
	run, err := LauncherRun("/bin/claude", nil, "", "/home/test", pfmconfig.ClaudePrefs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run, " -u CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT") {
		t.Fatalf("launcher run does not strip an inherited CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT: %q", run)
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
		run, err := claudeCommandWith(PurposeResume, hygieneNames, home, 1, false, machine, "--resume", "abc")
		if err != nil {
			t.Fatalf("mode %q: claudeCommandWith error = %v", testCase.mode, err)
		}
		if got := strings.Contains(run, " CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1"); got != testCase.wantLean {
			t.Fatalf("mode %q: lean env present=%v, want %v in %q", testCase.mode, got, testCase.wantLean, run)
		}
		if got := strings.Contains(
			run,
			" --system-prompt-file "+Quote(ProfessorPromptPath(home)),
		); got != testCase.wantFile {
			t.Fatalf("mode %q: professor flag present=%v, want %v in %q", testCase.mode, got, testCase.wantFile, run)
		}
	}
}
