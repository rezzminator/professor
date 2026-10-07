package action

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func TestQuoteRoundTripsHostileWords(t *testing.T) {
	values := []string{
		"",
		"plain",
		"space dir",
		"single'quote",
		"$HOME $(touch nope) `touch nope2`; newline\nnext",
		"tail\n",
	}
	for _, value := range values {
		outputPath := filepath.Join(t.TempDir(), "word")
		script := "set -- " + Quote(value) +
			"; printf %s \"$1\" > " + Quote(outputPath)
		command := exec.Command("sh", "-c", script)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("sh round trip %q: %v: %s", value, err, output)
		}
		content, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != value {
			t.Fatalf("Quote(%q) round trip = %q", value, content)
		}
	}
}

func TestLauncherRunAsWorkbench(t *testing.T) {
	request, dir := actionWorkbenchFixture(t, `{"prompt":"scribe.md","effort":"xhigh","model":"sonnet"}`)
	persona, err := workbench.ForLaunch(dir, pfmengine.Claude, workbench.New)
	if err != nil {
		t.Fatal(err)
	}
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{false: "persona", true: "zero"}[zero], func(t *testing.T) {
			selected := persona
			if zero {
				selected = workbench.Persona{}
			}
			got, err := LauncherRunAs(
				selected,
				"/bin/claude",
				nil,
				"",
				request.Home,
				pfmconfig.Config{},
				pfmconfig.ClaudePrefs{},
				"",
				"",
			)
			if err != nil {
				t.Fatal(err)
			}
			if zero {
				want, err := LauncherRun(
					"/bin/claude",
					nil,
					"",
					request.Home,
					pfmconfig.Config{},
					pfmconfig.ClaudePrefs{},
					"",
					"",
				)
				if err != nil || got != want {
					t.Fatalf("zero persona = %s, %v; want %s", got, err, want)
				}
				return
			}
			for _, pair := range []string{"'--system-prompt-file' " + Quote(persona.Prompt), "'--effort' 'xhigh'", "'--model' 'sonnet'"} {
				if !strings.Contains(got, pair) {
					t.Errorf("command lacks %s: %s", pair, got)
				}
			}
		})
	}
}

func TestSynthesizeRoutesAndEnvHygiene(t *testing.T) {
	home := t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	request := Request{
		Row: compose.Row{
			Kind: compose.ResumeClaude,
			ID:   id,
			CWD:  "/work/a project's $(dir)",
		},
		PrimaryAccount: 2,
		Cache1H:        true,
		Bunker:         true,
		Home:           home,
		FreshSocket:    "cc-1700000000-123-456",
	}
	plan, err := synthesizeWithTestConfig(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Route != ResumeClaude ||
		!strings.HasPrefix(plan.Line, "TMUX= exec tmux ") ||
		plan.ChatServer == nil || plan.ChatServer.CWD != request.Row.CWD {
		t.Fatalf("resume plan = %#v server = %#v", plan, plan.ChatServer)
	}
	parsed := parsedShell(t, plan.Run)
	if parsed.Resume != id || launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "1h" ||
		parsed.SettingsEnv[spawnDepthName] != "8" || plan.Record == nil || plan.Record.SessionID != id {
		t.Fatalf("resume=%q settings=%#v record=%#v", parsed.Resume, parsed.SettingsEnv, plan.Record)
	}
	// A resumed chat keeps full autonomy, on every account, and always
	// disables Claude Code's own output style so the staged prompt is the
	// only persona layer.
	if parsed.Settings["outputStyle"] != "default" || !parsed.Autonomy {
		t.Fatalf("resume run missed the settings flag or autonomy flags: %q", plan.Run)
	}
	for _, name := range []string{
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDECODE",
		"CLAUDE_CONFIG_DIR",
		"ENABLE_PROMPT_CACHING_1H",
		"FORCE_PROMPT_CACHING_5M",
		"CLAUDE_CODE_PROMPT_CACHE_TTL",
		"CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL",
		"CACHE_LIVE_CONTROL_MAIN_TTL",
		"CACHE_LIVE_CONTROL_AGENTS_TTL",
		// CC_ENDPOINT_UNSET — a chat born inside another
		// chat must never inherit a translating proxy's endpoint.
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_MODEL",
		"ANTHROPIC_SMALL_FAST_MODEL",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
		"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY",
	} {
		if !strings.Contains(plan.Run, "-u "+name) {
			t.Fatalf("resume run missed env scrub %s: %q", name, plan.Run)
		}
	}

	request.Row = compose.Row{
		Kind: compose.NewClaude,
		CWD:  "/rotated/project",
	}
	request.Cache1H = false
	request.Bunker = false
	plan, err = synthesizeWithTestConfig(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Line != attachLine(request.FreshSocket, request.FreshSocket, false) ||
		plan.ChatServer == nil || plan.ChatServer.Run != plan.Run || plan.ChatServer.CWD != request.Row.CWD {
		t.Fatalf("new Claude line = %q, server = %#v, run = %q", plan.Line, plan.ChatServer, plan.Run)
	}
	parsed = parsedShell(t, plan.Run)
	if parsed.SessionID == "" || launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "5m" ||
		parsed.Settings["outputStyle"] != "default" || !parsed.Autonomy || plan.Record == nil ||
		plan.Record.SessionID != parsed.SessionID {
		t.Fatalf("fresh id=%q settings=%#v record=%#v", parsed.SessionID, parsed.SettingsEnv, plan.Record)
	}
}

func TestAgentRouteCarriesCacheFlag(t *testing.T) {
	home := t.TempDir()
	plan, err := synthesizeWithTestConfig(Request{
		Row:            compose.Row{Kind: compose.Agent, ID: "22222222-2222-4222-8222-222222222222", CWD: "/work"},
		PrimaryAccount: 2, Cache1H: true, Home: home, FreshSocket: "cc-agent-cache",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Run, "internal agent-open --id") || !strings.Contains(plan.Run, " --cache 1h") ||
		strings.Contains(plan.Run, " ENABLE_PROMPT_CACHING_1H=") ||
		!strings.Contains(plan.Run, " CACHE_LIVE_CONTROL_MAIN_TTL='1h'") {
		t.Fatalf("agent route = %q", plan.Run)
	}
}

func TestDeadClaudeResumeUsesRecordedAccount(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		kind       compose.Kind
		rowAccount int
		want       int
	}{
		{"recorded resume", compose.ResumeClaude, 3, 3},
		{"missing record", compose.ResumeClaude, 0, 2},
		{"retired account", compose.ResumeClaude, 9, 2},
		{"agent fallback", compose.Agent, 3, 3},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			machine := testMachineConfig(home)
			machine.Accounts[2].Claude = &pfmconfig.ClaudePrefs{Binary: "/bin/claude-three"}
			plan, err := synthesizeWithTestConfig(Request{
				Row: compose.Row{
					Kind:    scenario.kind,
					ID:      "22222222-2222-4222-8222-222222222222",
					CWD:     "/work",
					Account: scenario.rowAccount,
				},
				PrimaryAccount: 2,
				Home:           home,
				FreshSocket:    "cc-account-test",
				Config:         machine,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantDir := pfmconfig.DefaultAccountDir(home, scenario.want)
			if !strings.Contains(plan.Run, "CLAUDE_CONFIG_DIR="+Quote(wantDir)) || plan.Record == nil ||
				plan.Record.Account != scenario.want {
				t.Fatalf("run = %q, record = %+v; want account %d", plan.Run, plan.Record, scenario.want)
			}
			if usesThird := strings.Contains(plan.Run, Quote("/bin/claude-three")); usesThird != (scenario.want == 3) {
				t.Fatalf("account %d effective binary in run = %t", scenario.want, usesThird)
			}
		})
	}
}

func TestSynthesizeRejectsAccountsOffTheRoster(t *testing.T) {
	home := t.TempDir()
	// An account outside the launcher's two-seat roster must never reach a
	// command line.
	for _, account := range []int{0, 4, 9} {
		_, err := synthesizeWithTestConfig(Request{
			Row: compose.Row{
				Kind: compose.NewClaude,
				CWD:  "/work/project",
			},
			PrimaryAccount: account,
			Home:           home,
			FreshSocket:    "cc-roster-test",
		})
		if err == nil || !strings.Contains(err.Error(), "requested Claude account") {
			t.Fatalf("account %d error = %v, want a roster rejection", account, err)
		}
	}
	for account := 1; account <= 3; account++ {
		if _, err := synthesizeWithTestConfig(Request{
			Row: compose.Row{
				Kind: compose.NewClaude,
				CWD:  "/work/project",
			},
			PrimaryAccount: account,
			Home:           home,
			FreshSocket:    "cc-roster-test",
		}); err != nil {
			t.Fatalf("account %d rejected: %v", account, err)
		}
	}
}

func TestAgentRouteUsesKilledInternalWiring(t *testing.T) {
	home := t.TempDir()
	plan, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind: compose.Agent,
			ID:   "33333333-3333-4333-8333-333333333333",
			CWD:  "/work/project",
		},
		PrimaryAccount: 1,
		Home:           home,
		FreshSocket:    "cc-1700000001-123-456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Run, "pfm internal agent-open") ||
		!strings.Contains(plan.Run, "--id") || !strings.Contains(plan.Run, "--cwd") {
		t.Fatalf("agent run = %q", plan.Run)
	}
}

func TestCodexLiveUsesOnlyVerifiedWindow(t *testing.T) {
	home := t.TempDir()
	row := compose.Row{
		Kind:        compose.LiveCodex,
		Socket:      "cx-1700000000-123-456",
		SessionName: "cx-session",
		Name:        strings.Repeat("界", 30),
	}
	plan, err := synthesizeWithTestConfig(Request{
		Row:            row,
		PrimaryAccount: 1,
		Home:           home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Line, Quote("cx-session")) ||
		strings.Contains(plan.Line, "cx-session:") {
		t.Fatalf("unverified Codex attach line = %q", plan.Line)
	}
	row.WindowName = "already-converged"
	plan, err = synthesizeWithTestConfig(Request{
		Row:            row,
		PrimaryAccount: 1,
		Home:           home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Line, Quote("cx-session:already-converged")) {
		t.Fatalf("Codex converged attach line = %q", plan.Line)
	}
}

// TestBootingRowAttachesLikeAnOrdinaryLiveRow proves Enter on a booting row
// takes the exact same Live attach route a normal live row does — the
// "existing Live attach synthesis" the fix promises, with no other operation
// reachable through this row's Kind.
func TestBootingRowAttachesLikeAnOrdinaryLiveRow(t *testing.T) {
	home := t.TempDir()
	bootingLine, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind:        compose.Booting,
			ID:          "cc-new-fixture-1",
			Socket:      "cc-new-fixture-1",
			SessionName: "cc-new-fixture-1",
		},
		PrimaryAccount: 1,
		Home:           home,
	})
	if err != nil {
		t.Fatal(err)
	}
	liveLine, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind:        compose.LiveClaude,
			Socket:      "cc-new-fixture-1",
			SessionName: "cc-new-fixture-1",
		},
		PrimaryAccount: 1,
		Home:           home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bootingLine.Route != Live {
		t.Fatalf("booting route = %v, want Live", bootingLine.Route)
	}
	if bootingLine.Line != liveLine.Line {
		t.Fatalf(
			"booting attach line = %q, want the ordinary live attach line %q",
			bootingLine.Line,
			liveLine.Line,
		)
	}
	if want := "TMUX= tmux -L " + Quote("cc-new-fixture-1") +
		" attach -t " + Quote("cc-new-fixture-1"); bootingLine.Line != want {
		t.Fatalf("booting attach line = %q, want %q", bootingLine.Line, want)
	}

	// A socket-less booting row (should never happen, but the guard is shared
	// with every other Live kind) still refuses cleanly rather than emitting a
	// bare "attach" with no target.
	if _, err := synthesizeWithTestConfig(Request{
		Row:            compose.Row{Kind: compose.Booting},
		PrimaryAccount: 1,
		Home:           home,
	}); err == nil {
		t.Fatal("socket-less booting row synthesized a plan instead of erroring")
	}
}

func TestAgentFailureNetFallsBackToSanitizedResume(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	pfmScript := filepath.Join(root, "pfm")
	claudeScript := filepath.Join(root, "claude")
	resultPath := filepath.Join(root, "result")
	writeActionFile(t, pfmScript, `#!/bin/sh
if [ "$1" = internal ] && [ "$2" = agent-open ]; then exit 1; fi
exit 2
`, 0o700)
	writeActionFile(t, claudeScript, `#!/bin/sh
{
  printf 'argv=%s\n' "$*"
  printf 'ttl=%s\n' "${CACHE_LIVE_CONTROL_MAIN_TTL-unset}"
  printf 'sid=%s\n' "${CLAUDE_CODE_SESSION_ID-unset}"
  printf 'code=%s\n' "${CLAUDECODE-unset}"
  printf 'cfg=%s\n' "${CLAUDE_CONFIG_DIR-unset}"
  printf 'enable=%s\n' "${ENABLE_PROMPT_CACHING_1H-unset}"
  printf 'force=%s\n' "${FORCE_PROMPT_CACHING_5M-unset}"
  printf 'base=%s\n' "${ANTHROPIC_BASE_URL-unset}"
  printf 'token=%s\n' "${ANTHROPIC_AUTH_TOKEN-unset}"
  printf 'gateway=%s\n' "${CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY-unset}"
} > "$ACTION_RESULT"
`, 0o700)
	id := "22222222-2222-4222-8222-222222222222"
	plan, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind:      compose.Agent,
			ID:        id,
			CWD:       "/work/agent",
			ConfigDir: pfmconfig.DefaultAccountDir(home, 2),
		},
		PrimaryAccount: 2,
		Cache1H:        true,
		Home:           home,
		FreshSocket:    "cc-1700000001-123-456",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", plan.Run)
	command.Env = append(
		os.Environ(),
		"PATH="+root+":"+os.Getenv("PATH"),
		"ACTION_RESULT="+resultPath,
		"CLAUDE_CODE_SESSION_ID=poison",
		"CLAUDECODE=poison",
		"CLAUDE_CONFIG_DIR=/poison",
		"ENABLE_PROMPT_CACHING_1H=poison",
		"FORCE_PROMPT_CACHING_5M=poison",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:9/proxy",
		"ANTHROPIC_AUTH_TOKEN=poison",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run agent failure net: %v: %s", err, output)
	}
	content, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	line := string(content)
	if !strings.HasPrefix(line, "argv=--resume "+id+" --settings ") ||
		!strings.Contains(line, "\nsid=unset\n") || !strings.Contains(line, "\ncode=unset\n") ||
		!strings.Contains(line, "\ncfg="+pfmconfig.DefaultAccountDir(home, 2)+"\n") ||
		!strings.Contains(line, "\nenable=unset\n") || !strings.Contains(line, "\nforce=unset\n") ||
		!strings.Contains(line, "\nbase=unset\n") || !strings.Contains(line, "\ntoken=unset\n") ||
		!strings.Contains(line, "\ngateway=unset\n") {
		t.Fatalf("fallback result = %q", content)
	}
	if !strings.Contains(line, "\nttl=1h\n") {
		t.Fatalf("fallback cache: want ttl=1h in the process environment, got %q", content)
	}
}

func TestSynthesizeRejectsNUL(t *testing.T) {
	home := t.TempDir()
	_, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind: compose.NewClaude,
			CWD:  "/work/a\x00b",
		},
		Home:           home,
		PrimaryAccount: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("NUL error = %v", err)
	}
}

func TestPickerLaunchPromptReachesClaudeCodexAndOpenCode(t *testing.T) {
	prompt := "Explain v0.61.2, ask for approval, then run pfm update."

	tests := []struct {
		name string
		row  compose.Row
		want []string
	}{
		{
			name: "Claude",
			row:  compose.Row{Kind: compose.NewClaude, CWD: "/work/.professor"},
			want: []string{"claude", Quote(prompt), "attach"},
		},
		{
			name: "Codex",
			row:  compose.Row{Kind: compose.NewCodex, CWD: "/work/.professor"},
			want: []string{"cx", Quote(prompt)},
		},
		{
			name: "OpenCode",
			row:  compose.Row{Kind: compose.NewOpenCode, CWD: "/work/.professor"},
			want: []string{"opencode", "--prompt", Quote(prompt)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			machine := testMachineConfig(home)
			machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{
				{ID: 1, Home: filepath.Join(home, ".local", "share", "opencode")},
			}
			plan, err := Synthesize(Request{
				Row: test.row, PrimaryAccount: 1, Home: home,
				FreshSocket: "engine-update-fixture", Config: machine,
				Prompt: prompt,
			})
			if err != nil {
				t.Fatal(err)
			}
			command := plan.Line + " " + plan.Run
			for _, want := range test.want {
				if !strings.Contains(command, want) {
					t.Fatalf("launch command %q lacks %q", command, want)
				}
			}
		})
	}
}

func TestPickerLaunchPromptCannotLeakIntoResumeOrLiveRoutes(t *testing.T) {
	home := t.TempDir()
	for _, row := range []compose.Row{
		{Kind: compose.ResumeClaude, ID: "11111111-1111-4111-8111-111111111111", CWD: "/work/project"},
		{Kind: compose.LiveClaude, ID: "11111111-1111-4111-8111-111111111111", Socket: "cc-live"},
	} {
		_, err := synthesizeWithTestConfig(Request{
			Row: row, PrimaryAccount: 1, Home: home,
			FreshSocket: "cc-new", Prompt: "must not disappear",
		})
		if err == nil || !strings.Contains(err.Error(), "initial prompt is not valid") {
			t.Fatalf("kind %s prompt error = %v", row.Kind, err)
		}
	}
}

func TestReaderGateUsesInjectedTerminalChannel(t *testing.T) {
	var output bytes.Buffer
	reboot, err := (ReaderGate{
		Reader: strings.NewReader("S"),
		Writer: &output,
	}).Confirm(context.Background(), GateRequest{
		Name:           "Builder",
		BirthAccount:   2,
		PrimaryAccount: 1,
		BirthCache1H:   false,
		WantCache1H:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reboot ||
		!strings.Contains(output.String(), "account  2 → 1") ||
		!strings.Contains(output.String(), "cache") {
		t.Fatalf("gate reboot=%v output=%q", reboot, output.String())
	}
}

func writeActionFile(
	t *testing.T,
	path, content string,
	mode os.FileMode,
) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// Every picker route that opens a chat on a fresh server is born through the
// ONE chat-server creator: the plan names the server for the executor to
// create detached, and the eval line only attaches to it. An attached
// `tmux new-session` in the line was a fourth creator — no title policy,
// automatic-rename left on, and a window the claude binary named "2.1.257".
func TestEveryFreshServerRouteIsBornThroughTheOneChatServerCreator(t *testing.T) {
	engineFor := map[Route]pfmengine.ID{
		NewClaude: pfmengine.Claude, Agent: pfmengine.Claude, ResumeClaude: pfmengine.Claude,
		NewOpenCode: pfmengine.OpenCode, ResumeOpenCode: pfmengine.OpenCode,
		ResumeCodex: pfmengine.Codex,
	}
	seen := make(map[Route]bool, len(engineFor))
	for _, request := range stressRequests(t.TempDir()) {
		plan, err := Synthesize(request)
		if err != nil {
			t.Fatal(err)
		}
		engine, born := engineFor[plan.Route]
		if !born {
			if plan.ChatServer != nil {
				t.Fatalf("route %c planned a chat server it never attaches to: %#v", plan.Route, plan.ChatServer)
			}
			continue
		}
		seen[plan.Route] = true
		server := plan.ChatServer
		if server == nil {
			t.Fatalf("route %c plans no chat server; line = %s", plan.Route, plan.Line)
		}
		if server.Socket != request.FreshSocket || server.CWD != request.Row.CWD || server.Run != plan.Run {
			t.Fatalf(
				"route %c server = %#v, want the fresh socket, the row's cwd and the plan's run",
				plan.Route,
				server,
			)
		}
		if want := pfmengine.MustLookup(engine).Short; server.Window != want {
			t.Fatalf("route %c window = %q, want %q", plan.Route, server.Window, want)
		}
		if server.Titles == nil || *server.Titles != request.Config.Tmux.Titles {
			t.Fatalf(
				"route %c titles = %v, want the machine's %v",
				plan.Route,
				server.Titles,
				request.Config.Tmux.Titles,
			)
		}
		prefix := "TMUX= "
		if request.Bunker {
			prefix += "exec "
		}
		socket := Quote(request.FreshSocket)
		if want := prefix + "tmux -L " + socket + " attach -t " + socket; plan.Line != want {
			t.Fatalf("route %c line = %s, want only the attach %s", plan.Route, plan.Line, want)
		}
	}
	for route := range engineFor {
		if !seen[route] {
			t.Fatalf("stress requests never reached route %c — the table proved nothing for it", route)
		}
	}
}

// TestLauncherIdentity is the shim's door: whatever a user types after
// `claude`, pfm may pin a fresh --session-id only when Claude starts a brand-new
// session, and may record a launch only under the id that session really has.
func TestLauncherIdentity(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		args       []string
		identity   string
		continuing bool
	}{
		{name: "fresh", args: []string{"--model", "opus"}},
		{name: "resume", args: []string{"--resume", "R"}, identity: "R"},
		{name: "resume equals", args: []string{"--resume=R"}, identity: "R"},
		{name: "short resume", args: []string{"-r", "R"}, identity: "R"},
		{name: "resume picker", args: []string{"--resume"}, continuing: true},
		{name: "short resume picker", args: []string{"-r"}, continuing: true},
		{name: "resume picker then flag", args: []string{"--resume", "--model", "opus"}, continuing: true},
		{name: "explicit", args: []string{"--session-id", "S"}, identity: "S"},
		{name: "continue", args: []string{"--continue"}, continuing: true},
		{name: "fork keeps parent unrecorded", args: []string{"--resume", "P", "--fork-session"}, continuing: true},
		{
			name:     "fork with its own id",
			args:     []string{"--resume", "P", "--fork-session", "--session-id", "F"},
			identity: "F",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			identity, _, continuing := LauncherIdentity(scenario.args)
			if identity != scenario.identity || continuing != scenario.continuing {
				t.Fatalf("LauncherIdentity(%q) = %q, continuing %t; want %q, %t",
					scenario.args, identity, continuing, scenario.identity, scenario.continuing)
			}
			run, err := LauncherRun(
				"/bin/claude",
				scenario.args,
				"",
				t.TempDir(),
				pfmconfig.Config{},
				pfmconfig.ClaudePrefs{},
				"",
			)
			if err != nil {
				t.Fatal(err)
			}
			fresh := scenario.identity == "" && !scenario.continuing
			added := strings.Count(
				run,
				"'--session-id'",
			) - strings.Count(
				strings.Join(scenario.args, " "),
				"--session-id",
			)
			if (fresh && added != 1) || (!fresh && added != 0) {
				t.Fatalf("pfm-added --session-id count %d (fresh=%t): %q", added, fresh, run)
			}
		})
	}
}

// TestLauncherRunCarriesTheMachineMCP pins that a plain `claude` typed in a
// shell (shim → pfm internal launch → LauncherRun) gets the same MCP servers
// as a pfm-rendered launch: no account's .claude.json carries them any more,
// so a launcher that drops the machine's MCP leaves the session without
// pfm's professor server and every mcp.thirdParty entry.
func TestLauncherRunCarriesTheMachineMCP(t *testing.T) {
	machine := pfmconfig.Config{
		MCPServers: map[string]pfmconfig.MCPServer{pfmconfig.MCPServerChat: {Enabled: true}},
		MCP: pfmconfig.MCPConfig{
			ThirdParty: map[string]json.RawMessage{"browser": json.RawMessage(`{"command":"browser-mcp"}`)},
		},
	}
	run, err := LauncherRun("/bin/claude", nil, t.TempDir(), t.TempDir(), machine, pfmconfig.ClaudePrefs{}, "")
	if err != nil {
		t.Fatal(err)
	}
	var servers struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	payload, err := os.ReadFile(parsedShell(t, run).MCPConfig)
	if err != nil {
		t.Fatalf("launcher mcp-config file unreadable: %v in %q", err, run)
	}
	if err := json.Unmarshal(payload, &servers); err != nil {
		t.Fatalf("launcher mcp-config unreadable: %v in %q", err, run)
	}
	if _, found := servers.MCPServers[pfmconfig.MCPServerProfessor]; !found {
		t.Fatalf("launcher drops pfm's professor server: %q", run)
	}
	if string(servers.MCPServers["browser"]) != `{"command":"browser-mcp"}` {
		t.Fatalf("launcher drops mcp.thirdParty browser: %q", run)
	}
}

func TestResumeRoutesCarryTheChatLabel(t *testing.T) {
	for _, scenario := range []struct {
		name string
		kind compose.Kind
		row  string
		want string
	}{
		{"resume with a label", compose.ResumeClaude, "Fix login", "'--name' 'Fix login'"},
		{"agent fallback with a label", compose.Agent, "Fix login", "'--name' 'Fix login'"},
		{"resume without a label", compose.ResumeClaude, "", ""},
		{"agent fallback without a label", compose.Agent, "", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			plan, err := synthesizeWithTestConfig(Request{
				Row: compose.Row{
					Kind: scenario.kind,
					ID:   "22222222-2222-4222-8222-222222222222",
					CWD:  "/work",
					Name: scenario.row,
				},
				PrimaryAccount: 2,
				Home:           home,
				FreshSocket:    "cc-label-test",
			})
			if err != nil {
				t.Fatal(err)
			}
			if scenario.want == "" {
				if strings.Contains(plan.Run, "'--name'") {
					t.Fatal("an unlabelled row carries --name")
				}
				return
			}
			if !strings.Contains(plan.Run, scenario.want) {
				t.Fatalf("the run does not carry %s", scenario.want)
			}
		})
	}
}

func TestLauncherResumeTarget(t *testing.T) {
	const id = "33333333-3333-4333-8333-333333333333"
	for _, scenario := range []struct {
		name string
		args []string
		want string
	}{
		{"resume", []string{"--resume", id}, id},
		{"short resume", []string{"-r", id}, id},
		{"resume equals", []string{"--resume=" + id}, id},
		{"fork from a transcript path", []string{
			"--session-id", "F", "--fork-session", "--resume", "/p/" + id + ".jsonl",
		}, id},
		{"resume equals a transcript path", []string{"--resume=/p/" + id + ".jsonl"}, id},
		{"bare resume", []string{"--resume"}, ""},
		{"resume picker then flag", []string{"--resume", "--model", "opus"}, ""},
		{"continue", []string{"--continue"}, ""},
		{"not a session id", []string{"--resume", "my-session"}, ""},
		{"transcript path of no session", []string{"--resume", "/p/notes.jsonl"}, ""},
		{"no resume", []string{"--model", "opus"}, ""},
		{"no args", nil, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := LauncherResumeTarget(scenario.args); got != scenario.want {
				t.Fatalf("LauncherResumeTarget(%q) = %q, want %q", scenario.args, got, scenario.want)
			}
		})
	}
}

func TestLauncherRunCarriesTheChatLabel(t *testing.T) {
	args := []string{"--resume", "33333333-3333-4333-8333-333333333333", "--fork-session"}
	home := t.TempDir()
	named, err := LauncherRun("/bin/claude", args, "", home, pfmconfig.Config{}, pfmconfig.ClaudePrefs{}, "Fix login")
	if err != nil {
		t.Fatal(err)
	}
	if got := parsedShell(t, named).Name; got != "Fix login" {
		t.Fatalf("launcher --name = %q, want %q", got, "Fix login")
	}
	plain, err := LauncherRun("/bin/claude", args, "", home, pfmconfig.Config{}, pfmconfig.ClaudePrefs{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "'--name'") {
		t.Fatal("an unlabelled launcher run carries --name")
	}
}
