package action

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func mustProfessorPromptPath(t *testing.T, home string) string {
	t.Helper()
	path, err := ProfessorPromptPath(home)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// stageProfessorPrompt writes a prompt into a private fixture clone.
func stageProfessorPrompt(t *testing.T, home string) string {
	t.Helper()
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	path := mustProfessorPromptPath(t, home)
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
		Quote(machine.Claude.Binary),
		Quote(prompt),
	} {
		if !strings.Contains(plan.Run, want) {
			t.Fatalf("native fresh run %q lacks %q", plan.Run, want)
		}
	}
	parsed := parsedShell(t, plan.Run)
	if parsed.SessionID == "" || parsed.PromptFile != mustProfessorPromptPath(t, home) ||
		launchEnv(t, plan.Run)["CLAUDE_CODE_PROMPT_CACHE_TTL"] != "5m" || plan.Record == nil ||
		plan.Record.SessionID != parsed.SessionID || plan.Record.Account != 42 {
		t.Fatalf("fresh launch id=%q prompt=%q record=%#v", parsed.SessionID, parsed.PromptFile, plan.Record)
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
	for _, retired := range []string{" cc42", "_cc_run"} {
		if strings.Contains(plan.Line, retired) || strings.Contains(plan.Run, retired) {
			t.Fatalf("native fresh action retained retired shell surface %q: %#v", retired, plan)
		}
	}
}

// TestNewClaudeNativeSpawnOmitsMissingSystemPromptFile is F3/F4's regression
// test: a fresh account chose SystemPromptProfessor, but the home has no
// source-repo marker (a brand-new machine). The
// door must degrade to the lean fallback rather than pass claude a
// --system-prompt-file flag pointing at nothing, which would brick the
// launch instead of merely losing the extra prompt material — the fail-open
// contract promptFile's doc comment states.
func TestNewClaudeNativeSpawnOmitsMissingSystemPromptFile(t *testing.T) {
	home := t.TempDir()
	// Deliberately leave this home without a source-repo marker.
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
		t.Fatalf("native fresh run carried --system-prompt-file for a missing composed prompt: %q", plan.Run)
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
	parsed := parsedShell(t, plan.Run)
	if launchEnv(t, plan.Run)["CLAUDE_CODE_PROMPT_CACHE_TTL"] != "1h" ||
		parsed.SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] != "1" || !parsed.Autonomy {
		t.Fatalf("native fresh settings=%#v autonomy=%t", parsed.SettingsEnv, parsed.Autonomy)
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
