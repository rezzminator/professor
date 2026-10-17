package action

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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
		launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "5m" || plan.Record == nil ||
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
	if launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "1h" ||
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
	home := t.TempDir()
	_, err := Synthesize(Request{
		Row:            compose.Row{Kind: compose.NewClaude, CWD: "/work/project"},
		PrimaryAccount: 1,
		Home:           home,
		Config:         testMachineConfig(home),
	})
	if err == nil || !strings.Contains(err.Error(), "fresh socket") {
		t.Fatalf("missing fresh socket error = %v", err)
	}
}

// The picker's plan carries what the starter must be told: a professor-mode
// chat opened without the fleet prompt, whichever way it opens.
func TestSynthesizeClaudePlansWarnWhenTheFleetPromptCannotReachTheChat(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, kind := range []compose.Kind{compose.NewClaude, compose.ResumeClaude, compose.Agent} {
		for _, staged := range []bool{false, true} {
			t.Run(kind.String()+"/staged="+strconv.FormatBool(staged), func(t *testing.T) {
				home := t.TempDir()
				if staged {
					stageProfessorPrompt(t, home)
				}
				machine := configuredMachinePolicy(home)
				machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
				plan, err := Synthesize(Request{
					Row:            compose.Row{Kind: kind, ID: id, CWD: "/work/project"},
					PrimaryAccount: 42, Home: home, FreshSocket: "cc-warn", Config: machine,
				})
				if err != nil {
					t.Fatal(err)
				}
				if staged {
					if len(plan.Warnings) != 0 {
						t.Fatalf("warnings = %q, want none", plan.Warnings)
					}
					return
				}
				if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "no source repository recorded") {
					t.Fatalf("warnings = %q, want one naming the missing clone", plan.Warnings)
				}
			})
		}
	}
}

func TestHerdrPickerPreparesSamePaneSeatHost(t *testing.T) {
	testjail.Fleet(t)
	socket := herdrPickerSocket(t)
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w6:p3")
	t.Setenv("TMUX", "")
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Claude.Binary = filepath.Join(home, "native-claude")
	if err := testjail.WriteExecutable(machine.Claude.Binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	executor, err := New(Dependencies{Tmux: &fakeActionTmux{alive: map[string]bool{}}, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	line, err := executor.Open(
		context.Background(),
		Request{
			Row:            compose.Row{Kind: compose.NewClaude, CWD: "/work/project"},
			Home:           home,
			PrimaryAccount: 42,
			FreshSocket:    "cc-unused",
			Config:         machine,
		},
	)
	if err != nil || !strings.HasPrefix(line, "exec pfm internal seat-host ") {
		t.Fatalf("line=%q err=%v", line, err)
	}
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	seat, found, err := state.Seat(context.Background(), strings.TrimPrefix(line, "exec pfm internal seat-host "))
	if err != nil || !found || seat.Backend != "herdr" || seat.HerdrPane != "w6:p3" {
		t.Fatalf("seat=%+v found=%v err=%v", seat, found, err)
	}
}

func herdrPickerSocket(t *testing.T) string {
	t.Helper()
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID string `json:"id"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				_, _ = fmt.Fprintf(conn, `{"id":%q,"ok":true,"result":{"pane":{"pane_id":"w6:p3"}}}`+"\n", req.ID)
			}()
		}
	}()
	return socket
}
