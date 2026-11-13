package action

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func TestLauncherRunAsWorkbench(t *testing.T) {
	request, dir := actionWorkbenchFixture(t, `{"prompt":"scribe.md","effort":"xhigh","model":"sonnet"}`)
	persona, err := workbench.ForLaunch(dir, "", pfmengine.Claude, workbench.New)
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
			for _, pair := range []string{"'--system-prompt-file' " + cmdparse.Quote(persona.Prompt), "'--effort' 'xhigh'", "'--model' 'sonnet'"} {
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
	if plan.Line != AttachLine(request.FreshSocket, request.FreshSocket, false) ||
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

// The agent route runs pfm's killed-internal agent-open wiring and carries the
// one-hour cache as its flag and process env, never the legacy cache arm.
func TestAgentRouteCarriesCacheFlag(t *testing.T) {
	home := t.TempDir()
	var runs strings.Builder
	for _, cache := range []bool{false, true} {
		plan, err := synthesizeWithTestConfig(Request{
			Row:            compose.Row{Kind: compose.Agent, ID: "22222222-2222-4222-8222-222222222222", CWD: "/work"},
			PrimaryAccount: 2, Cache1H: cache, Home: home, FreshSocket: "cc-agent-cache",
		})
		if err != nil {
			t.Fatal(err)
		}
		runs.WriteString(plan.Run + "\n")
	}
	actionGolden(t, "action_agent_route.txt", rooted(t, runs.String()))
}

func TestDeadClaudeResumeUsesRecordedAccount(t *testing.T) {
	var runs strings.Builder
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
		{
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
			if plan.Record == nil || plan.Record.Account != scenario.want {
				t.Errorf("%s: record = %+v, want account %d", scenario.name, plan.Record, scenario.want)
			}
			fmt.Fprintf(&runs, "%s: %s\n", scenario.name, rooted(t, plan.Run))
		}
	}
	actionGolden(t, "action_dead_claude_resume_accounts.txt", runs.String())
}

func TestSynthesizeRejectsAccountsOffTheRoster(t *testing.T) {
	home := t.TempDir()
	// An account outside the launcher's two-seat roster must never reach a
	// command line.
	for account, want := range map[int]string{0: "requested Claude account 0 is not in the configured roster", 4: "requested Claude account 4 is not in the configured roster", 9: "requested Claude account 9 is not in the configured roster"} {
		_, err := synthesizeWithTestConfig(Request{
			Row: compose.Row{
				Kind: compose.NewClaude,
				CWD:  "/work/project",
			},
			PrimaryAccount: account,
			Home:           home,
			FreshSocket:    "cc-roster-test",
		})
		if got := fmt.Sprint(err); got != want {
			t.Errorf("account %d error = %q, want %q", account, got, want)
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
	if plan.Line != "TMUX= tmux -L 'cx-1700000000-123-456' attach -t 'cx-session'" {
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
	if plan.Line != "TMUX= tmux -L 'cx-1700000000-123-456' attach -t 'cx-session:already-converged'" {
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
	if want := "TMUX= tmux -L " + cmdparse.Quote("cc-new-fixture-1") +
		" attach -t " + cmdparse.Quote("cc-new-fixture-1"); bootingLine.Line != want {
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
	if err == nil || err.Error() != "action values cannot contain NUL" {
		t.Fatalf("NUL error = %v", err)
	}
}

func TestPickerLaunchPromptReachesClaudeCodexAndOpenCode(t *testing.T) {
	prompt := "Explain v0.61.2, ask for approval, then run pfm update."
	home := t.TempDir()
	machine := testMachineConfig(home)
	machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{
		{ID: 1, Home: filepath.Join(home, ".local", "share", "opencode")},
	}
	var commands strings.Builder
	for _, kind := range []compose.Kind{compose.NewClaude, compose.NewCodex, compose.NewOpenCode} {
		plan, err := Synthesize(Request{
			Row: compose.Row{Kind: kind, CWD: "/work/.professor"}, PrimaryAccount: 1, Home: home,
			FreshSocket: "engine-update-fixture", Config: machine,
			Prompt: prompt,
		})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&commands, "%s\n%s\n", plan.Line, plan.Run)
	}
	actionGolden(t, "action_picker_launch_prompt.txt", rooted(t, commands.String()))
}

func TestPickerLaunchPromptCannotLeakIntoResumeOrLiveRoutes(t *testing.T) {
	home := t.TempDir()
	for want, row := range map[string]compose.Row{
		"initial prompt is not valid for resume-claude route": {Kind: compose.ResumeClaude, ID: "11111111-1111-4111-8111-111111111111", CWD: "/work/project"},
		"initial prompt is not valid for live-claude route":   {Kind: compose.LiveClaude, ID: "11111111-1111-4111-8111-111111111111", Socket: "cc-live"},
	} {
		_, err := synthesizeWithTestConfig(Request{
			Row: row, PrimaryAccount: 1, Home: home,
			FreshSocket: "cc-new", Prompt: "must not disappear",
		})
		if got := fmt.Sprint(err); got != want {
			t.Errorf("kind %s prompt error = %q, want %q", row.Kind, got, want)
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
		output.String() != `
⚠ birth env ≠ picker — Builder is live
  account  2 → 1
  cache    🪫 5m → ⚡ 1h
Enter attaches as-is · s reboots to match
❯ 
` {
		t.Fatalf("gate reboot=%v output=%q", reboot, output.String())
	}
}

func writeActionFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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
		socket := cmdparse.Quote(request.FreshSocket)
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
	if _, found := servers.MCPServers[pfmconfig.MCPRegistrationName(pfmconfig.MCPServerChat)]; !found {
		t.Fatalf("launcher drops pfm's professor server: %q", run)
	}
	if string(servers.MCPServers["browser"]) != `{"command":"browser-mcp"}` {
		t.Fatalf("launcher drops mcp.thirdParty browser: %q", run)
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

// Every action door that starts a Codex TUI runs it in-process, off the shared app-server daemon whose
// hooks reach no pane: chat new and the MCP new tool (PlanCodex), chat open, the picker's and FleetView's
// resume and Herdr's agent_resume (ResumeCodex), chat branch (HeadlessFork). The flag sits right after
// the binary word; these doors carry only pfm-built arguments, so no user transport can arrive.
func TestCodexDoorsRunInProcess(t *testing.T) {
	home := t.TempDir()
	machine := testMachineConfig(home)
	machine.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}
	machine.Codex.Yolo = false
	newPlan, err := HeadlessRun(HeadlessRequest{
		Engine: pfmengine.Codex, Name: "worker", CWD: "/work", Home: home, PrimaryAccount: 1, Config: machine,
	})
	if err != nil {
		t.Fatalf("PlanCodex: %v", err)
	}
	resume := stressRequests(home)[0]
	resume.Config = machine
	resume.Row = compose.Row{
		Kind: compose.ResumeCodex,
		CWD:  "/work/project",
		ID:   "44444444-4444-4444-8444-444444444444",
	}
	resumePlan, err := Synthesize(resume)
	if err != nil {
		t.Fatalf("ResumeCodex: %v", err)
	}
	forkPlan, err := HeadlessFork(HeadlessForkRequest{
		Engine: pfmengine.Codex, SessionID: "thread-123", Name: "branch", CWD: "/work",
		Home: home, PrimaryAccount: 1, Config: machine,
	})
	if err != nil {
		t.Fatalf("HeadlessFork: %v", err)
	}
	if got, want := rooted(
		t,
		newPlan.Run+"\n"+resumePlan.Run+"\n"+forkPlan.Run,
	), "env -u CLAUDE_CODE_SESSION_ID -u CLAUDECODE -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CONFIG_DIR -u PFM_CLAUDE_CONFIG_DIR_DEFAULT -u CLAUDE_PROJECT_DIR -u ENABLE_PROMPT_CACHING_1H -u FORCE_PROMPT_CACHING_5M -u CLAUDE_CODE_PROMPT_CACHE_TTL -u CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL -u CACHE_LIVE_CONTROL_MAIN_TTL -u CACHE_LIVE_CONTROL_AGENTS_TTL -u CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT -u ANTHROPIC_BASE_URL -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_API_KEY -u ANTHROPIC_MODEL -u ANTHROPIC_SMALL_FAST_MODEL -u CLAUDE_CODE_AUTO_COMPACT_WINDOW -u CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC -u CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK -u CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY -u CODEX_THREAD_ID CODEX_HOME='{root}/001/.codex' codex --no-daemon --sandbox workspace-write\nprintf '%s\\n' '═══ RESUME of 44444444-4444-4444-8444-444444444444 — verify before trusting this pane ═══' '    Loaded? Read the status line: no context/token counts means Codex did NOT rehydrate this thread.' '    Scrollback is never restored — the prior tmux server is reaped on kill.' '    Came up empty? The rollout is whole: pfm chat recover 44444444-4444-4444-8444-444444444444'; env -u CLAUDE_CODE_SESSION_ID -u CLAUDECODE -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CONFIG_DIR -u PFM_CLAUDE_CONFIG_DIR_DEFAULT -u CLAUDE_PROJECT_DIR -u ENABLE_PROMPT_CACHING_1H -u FORCE_PROMPT_CACHING_5M -u CLAUDE_CODE_PROMPT_CACHE_TTL -u CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL -u CACHE_LIVE_CONTROL_MAIN_TTL -u CACHE_LIVE_CONTROL_AGENTS_TTL -u CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT -u ANTHROPIC_BASE_URL -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_API_KEY -u ANTHROPIC_MODEL -u ANTHROPIC_SMALL_FAST_MODEL -u CLAUDE_CODE_AUTO_COMPACT_WINDOW -u CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC -u CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK -u CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY -u CODEX_THREAD_ID CODEX_HOME='{root}/001/.codex' codex --no-daemon --sandbox workspace-write 'resume' '44444444-4444-4444-8444-444444444444'\nenv -u CLAUDE_CODE_SESSION_ID -u CLAUDECODE -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CONFIG_DIR -u PFM_CLAUDE_CONFIG_DIR_DEFAULT -u CLAUDE_PROJECT_DIR -u ENABLE_PROMPT_CACHING_1H -u FORCE_PROMPT_CACHING_5M -u CLAUDE_CODE_PROMPT_CACHE_TTL -u CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL -u CACHE_LIVE_CONTROL_MAIN_TTL -u CACHE_LIVE_CONTROL_AGENTS_TTL -u CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT -u ANTHROPIC_BASE_URL -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_API_KEY -u ANTHROPIC_MODEL -u ANTHROPIC_SMALL_FAST_MODEL -u CLAUDE_CODE_AUTO_COMPACT_WINDOW -u CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC -u CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK -u CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY -u CODEX_THREAD_ID CODEX_HOME='{root}/001/.codex' codex --no-daemon --sandbox workspace-write 'fork' 'thread-123'"; got != want {
		t.Errorf("chat new, open and branch Codex runs = %q, want %q", got, want)
	}
}

func TestSynthesizeRawNativeRecipesRetainTypedLaunch(t *testing.T) {
	for _, kind := range []compose.Kind{compose.NewCodex, compose.NewOpenCode} {
		t.Run(kind.String(), func(t *testing.T) {
			home := t.TempDir()
			machine := testMachineConfig(home)
			machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 2, Home: home + "/opencode"}}
			machine.OpenCode.Binary = "/opt/open code"
			machine.Codex.Binary = "/opt/codex stable"
			t.Setenv("CLAUDECODE", "stale")
			t.Setenv("CLAUDE_CODE_SSE_PORT", "stale-port")
			request := Request{
				Row:            compose.Row{Kind: kind, CWD: "/work/native"},
				Home:           home,
				PrimaryAccount: 2,
				FreshSocket:    "unused",
				Prompt:         "a 'quoted' prompt",
				Config:         machine,
			}
			plan, err := Synthesize(request)
			if err != nil {
				t.Fatal(err)
			}
			if plan.SeatRun == nil || plan.SeatRun.CWD != request.Row.CWD ||
				!slices.Contains(plan.SeatRun.Args, request.Prompt) {
				t.Fatalf("raw native launch=%+v", plan.SeatRun)
			}
			if lastEnvironmentValueNative(plan.SeatRun.Env, "CLAUDECODE") != "" {
				t.Fatalf("stale identity in native environment: %v", plan.SeatRun.Env)
			}
			if kind == compose.NewCodex {
				account, _ := machine.CodexAccountByID(2)
				if plan.SeatRun.Binary != machine.Codex.Binary || plan.SeatRun.Args[0] != pfmengine.CodexNoDaemon ||
					lastEnvironmentValueNative(plan.SeatRun.Env, "CODEX_HOME") != account.Home {
					t.Fatalf("Codex native recipe=%+v", plan.SeatRun)
				}
			} else if plan.SeatRun.Binary != machine.OpenCode.Binary ||
				lastEnvironmentValueNative(plan.SeatRun.Env, "CLAUDE_CODE_SSE_PORT") != "" ||
				!slices.Contains(plan.SeatRun.Args, "--prompt") {
				t.Fatalf("OpenCode native recipe=%+v", plan.SeatRun)
			}
		})
	}
}
