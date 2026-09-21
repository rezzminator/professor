package action

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// stageProfessorPrompt writes the staged professor prompt file a
// SystemPromptProfessor spawn reads via ProfessorPromptPath, so a test can
// exercise the "file present" side of promptFile's absence guard.
func stageProfessorPrompt(t *testing.T, home string) string {
	t.Helper()
	path := ProfessorPromptPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("stage professor prompt dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("professor prompt\n"), 0o644); err != nil {
		t.Fatalf("stage professor prompt file: %v", err)
	}
	return path
}

func TestNewClaudeUsesNativeConfiguredSpawn(t *testing.T) {
	home := t.TempDir()
	stageProfessorPrompt(t, home)
	machine := configuredMachinePolicy(home)
	machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
	prompt := "fresh prompt with '$HOME' and $(touch nope)"
	request := Request{
		Row: compose.Row{
			Kind: compose.NewClaude,
			CWD:  "/work/project with spaces",
		},
		PrimaryAccount: 42,
		Cache1H:        false,
		Bunker:         true,
		Home:           home,
		FreshSocket:    "cc-native-42",
		Config:         machine,
		Prompt:         prompt,
	}
	plan, err := Synthesize(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CLAUDE_CONFIG_DIR=" + Quote(machine.Accounts[0].ConfigDir),
		"FORCE_PROMPT_CACHING_5M=1",
		Quote(machine.Claude.Binary),
		Quote(prompt),
		"--system-prompt-file " + Quote(ProfessorPromptPath(home)),
	} {
		if !strings.Contains(plan.Run, want) {
			t.Fatalf("native fresh run %q lacks %q", plan.Run, want)
		}
	}
	if strings.Contains(plan.Run, "skip-permissions") {
		t.Fatalf("prompted account received autonomy bypass: %q", plan.Run)
	}
	if got, want := plan.Line, attachLine(request.FreshSocket, request.FreshSocket, true); got != want {
		t.Fatalf("native fresh line = %q, want bunker attach line %q", got, want)
	}
	if plan.ChatServer == nil || plan.ChatServer.Run != plan.Run || plan.ChatServer.CWD != request.Row.CWD {
		t.Fatalf("native fresh server = %#v, want the plan's run in the row's cwd", plan.ChatServer)
	}
	for _, retired := range []string{" cc42", "_cc_run", "CC_ARM_1H"} {
		if strings.Contains(plan.Line, retired) || strings.Contains(plan.Run, retired) {
			t.Fatalf("native fresh action retained retired shell surface %q: %#v", retired, plan)
		}
	}
}

// TestNewClaudeNativeSpawnOmitsMissingSystemPromptFile is F3/F4's regression
// test: a fresh account chose SystemPromptProfessor, but pfm install never
// staged the prompt file (a brand-new machine, a wiped .local/share). The
// door must degrade to the lean fallback rather than pass claude a
// --system-prompt-file flag pointing at nothing, which would brick the
// launch instead of merely losing the extra prompt material — the fail-open
// contract promptFile's doc comment states.
func TestNewClaudeNativeSpawnOmitsMissingSystemPromptFile(t *testing.T) {
	home := t.TempDir()
	// Deliberately NOT staging the professor prompt file under home.
	machine := configuredMachinePolicy(home)
	machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
	request := Request{
		Row: compose.Row{
			Kind: compose.NewClaude,
			CWD:  "/work/project with spaces",
		},
		PrimaryAccount: 42,
		Home:           home,
		FreshSocket:    "cc-native-42",
		Config:         machine,
	}
	plan, err := Synthesize(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Run, "--system-prompt-file") {
		t.Fatalf("native fresh run carried --system-prompt-file for a missing staged prompt: %q", plan.Run)
	}
}

func TestNewClaudeNativeSpawnPreservesBypassLeanAndCachePolicy(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Claude.PermissionMode = pfmconfig.PermissionBypass
	machine.Claude.SystemPrompt = pfmconfig.SystemPromptLean
	request := Request{
		Row:            compose.Row{Kind: compose.NewClaude, CWD: "/work/project"},
		PrimaryAccount: 42,
		Cache1H:        true,
		Home:           home,
		FreshSocket:    "cc-native-policy",
		Config:         machine,
	}
	plan, err := Synthesize(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ENABLE_PROMPT_CACHING_1H=1",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1",
		autonomyFlags,
	} {
		if !strings.Contains(plan.Run, want) {
			t.Fatalf("native fresh run %q lacks %q", plan.Run, want)
		}
	}
	if strings.Contains(plan.Run, "--system-prompt-file") {
		t.Fatalf("lean prompt mode also received professor prompt file: %q", plan.Run)
	}
	if strings.HasPrefix(plan.Line, "TMUX= exec ") {
		t.Fatalf("non-bunker fresh launch used exec prefix: %q", plan.Line)
	}
}

func TestNewClaudeRequiresFreshSocket(t *testing.T) {
	_, err := Synthesize(Request{
		Row:            compose.Row{Kind: compose.NewClaude, CWD: "/work/project"},
		PrimaryAccount: 1,
		Home:           "/home/test",
		Config:         testMachineConfig("/home/test"),
	})
	if err == nil || !strings.Contains(err.Error(), "fresh socket") {
		t.Fatalf("missing fresh socket error = %v", err)
	}
}
