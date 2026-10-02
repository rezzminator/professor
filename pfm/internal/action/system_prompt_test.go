package action

import (
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
	want := filepath.Join(repo, "pfm", "harness-prompts", "composed", "claude.md")
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
			pfmconfig.Config{}, pfmconfig.ClaudePrefs{SystemPrompt: testCase.mode},
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
	run, err := LauncherRun("/bin/claude", nil, "", "/home/test", pfmconfig.Config{}, pfmconfig.ClaudePrefs{})
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
		parsed := parsedShell(t, run)
		if got := parsed.SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] == "1"; got != testCase.wantLean {
			t.Fatalf("mode %q: lean settings present=%v, want %v", testCase.mode, got, testCase.wantLean)
		}
		if got := parsed.PromptFile == mustProfessorPromptPath(t, home); got != testCase.wantFile {
			t.Fatalf("mode %q: professor flag present=%v, want %v", testCase.mode, got, testCase.wantFile)
		}
	}
}
