package action

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
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

func TestSynthesizeRoutesAndEnvHygiene(t *testing.T) {
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
		Home:           "/home/test",
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
	wantPrefix := hygiene +
		" CLAUDE_CONFIG_DIR='/home/test/.cc/2'" +
		" ENABLE_PROMPT_CACHING_1H=1" +
		" " + webSearchBudgetName + "=" + Quote(webSearchBudgetValue) +
		" claude"
	if !strings.HasPrefix(plan.Run, wantPrefix) {
		t.Fatalf("resume run = %q, want prefix %q", plan.Run, wantPrefix)
	}
	// A resumed chat keeps full autonomy, on every account, and always
	// disables Claude Code's own output style so the staged prompt is the
	// only persona layer.
	if !strings.Contains(
		plan.Run,
		"claude '--resume' "+Quote(id)+" '--settings' "+Quote(pfmengine.OutputStyleDefaultSettings)+" "+autonomyFlags,
	) {
		t.Fatalf("resume run missed the settings flag or autonomy flags: %q", plan.Run)
	}
	for _, name := range []string{
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDECODE",
		"CLAUDE_CONFIG_DIR",
		"ENABLE_PROMPT_CACHING_1H",
		"FORCE_PROMPT_CACHING_5M",
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
	for _, want := range []string{
		"CLAUDE_CONFIG_DIR='/home/test/.cc/2'",
		"FORCE_PROMPT_CACHING_5M=1",
		"'--settings' " + Quote(pfmengine.OutputStyleDefaultSettings),
		autonomyFlags,
	} {
		if !strings.Contains(plan.Run, want) {
			t.Fatalf("new Claude run %q lacks %q", plan.Run, want)
		}
	}
}

func TestSynthesizeRejectsAccountsOffTheRoster(t *testing.T) {
	// An account outside the launcher's two-seat roster must never reach a
	// command line.
	for _, account := range []int{0, 4, 9} {
		_, err := synthesizeWithTestConfig(Request{
			Row: compose.Row{
				Kind: compose.NewClaude,
				CWD:  "/work/project",
			},
			PrimaryAccount: account,
			Home:           "/home/test",
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
			Home:           "/home/test",
			FreshSocket:    "cc-roster-test",
		}); err != nil {
			t.Fatalf("account %d rejected: %v", account, err)
		}
	}
}

func TestAgentRouteUsesKilledInternalWiring(t *testing.T) {
	plan, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind: compose.Agent,
			ID:   "33333333-3333-4333-8333-333333333333",
			CWD:  "/work/project",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
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
	row := compose.Row{
		Kind:        compose.LiveCodex,
		Socket:      "cx-1700000000-123-456",
		SessionName: "cx-session",
		Name:        strings.Repeat("界", 30),
	}
	plan, err := synthesizeWithTestConfig(Request{
		Row:            row,
		PrimaryAccount: 1,
		Home:           "/home/test",
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
		Home:           "/home/test",
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
	bootingLine, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind:        compose.Booting,
			ID:          "cc-new-fixture-1",
			Socket:      "cc-new-fixture-1",
			SessionName: "cc-new-fixture-1",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
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
		Home:           "/home/test",
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
		Home:           "/home/test",
	}); err == nil {
		t.Fatal("socket-less booting row synthesized a plan instead of erroring")
	}
}

func TestAgentFailureNetFallsBackToSanitizedResume(t *testing.T) {
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
			ConfigDir: "/home/test/.cc/2",
		},
		PrimaryAccount: 2,
		Cache1H:        true,
		Home:           "/home/test",
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
	want := strings.Join([]string{
		"argv=--resume " + id + " --settings " + pfmengine.OutputStyleDefaultSettings + " " + autonomyFlags,
		"sid=unset",
		"code=unset",
		"cfg=/home/test/.cc/2",
		"enable=1",
		"force=unset",
		"base=unset",
		"token=unset",
		"gateway=unset",
		"",
	}, "\n")
	if string(content) != want {
		t.Fatalf("fallback result = %q, want %q", content, want)
	}
}

func TestSynthesizeRejectsNUL(t *testing.T) {
	_, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind: compose.NewClaude,
			CWD:  "/work/a\x00b",
		},
		PrimaryAccount: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("NUL error = %v", err)
	}
}

func TestPickerLaunchPromptReachesClaudeCodexAndOpenCode(t *testing.T) {
	prompt := "Explain v0.61.2, ask for approval, then run pfm update."
	machine := testMachineConfig("/home/test")
	machine.OpencodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 1, Home: "/home/test/.local/share/opencode"}}

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
			row:  compose.Row{Kind: compose.NewOpencode, CWD: "/work/.professor"},
			want: []string{"opencode", "--prompt", Quote(prompt)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := Synthesize(Request{
				Row: test.row, PrimaryAccount: 1, Home: "/home/test",
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
	for _, row := range []compose.Row{
		{Kind: compose.ResumeClaude, ID: "11111111-1111-4111-8111-111111111111", CWD: "/work/project"},
		{Kind: compose.LiveClaude, ID: "11111111-1111-4111-8111-111111111111", Socket: "cc-live"},
	} {
		_, err := synthesizeWithTestConfig(Request{
			Row: row, PrimaryAccount: 1, Home: "/home/test",
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
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
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
		NewOpencode: pfmengine.Opencode, ResumeOpencode: pfmengine.Opencode,
		ResumeCodex: pfmengine.Codex,
	}
	seen := make(map[Route]bool, len(engineFor))
	for _, request := range stressRequests() {
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
