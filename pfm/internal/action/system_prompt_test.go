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
		{"professor, no clone recorded", pfmconfig.SystemPromptProfessor, false, nil, "no source repository recorded"},
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
			if entry.want == "" && got != "" || !strings.Contains(got, entry.want) {
				t.Fatalf("LauncherLaunchAs warning = %q, want %q", got, entry.want)
			}
		})
	}
}
