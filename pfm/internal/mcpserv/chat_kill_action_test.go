package mcpserv

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

// TestChatKillSurfacesAKilledButStillAliveFailure is the MCP-side half of the
// chat_kill fix: the tool must never answer status "ok" while the target's
// engine process is still running. cliAction already maps a non-zero CLI
// exit to a tool error (internal/mcpserv/actions.go); this pins that chat_kill
// specifically relies on it, so runResolvedChatKill's ConfirmExit failure
// (cmd/pfm/chat_command.go) reaches the caller as a tool error, not "ok".
func TestChatKillSurfacesAKilledButStillAliveFailure(t *testing.T) {
	service := &Service{backend: &backend{
		dispatch: func(_ context.Context, _ []string, _, stderr io.Writer) int {
			_, _ = io.WriteString(stderr, "pfm chat kill: pane %3 still alive after kill\n")
			return 1
		},
	}}

	_, output, err := service.chatKill(context.Background(), nil, KillInput{Target: "my-chat", Exit: true})
	if err == nil {
		t.Fatal("chatKill err = nil, want the CLI's non-zero exit surfaced as a tool error")
	}
	if !strings.Contains(err.Error(), "still alive") {
		t.Fatalf("chatKill err = %v, want it to carry the still-alive reason", err)
	}
	if output.Status != statusError {
		t.Fatalf("chatKill status = %q, want %q — never ok over a live pane", output.Status, statusError)
	}
}

// A CLI verb that succeeds can still have something to say: `pfm chat kill`
// writes "recording the kill without closing it" to STDERR and exits 0 when
// the target resolved to no live pane. cliAction used to return only stdout
// on a zero exit, so the MCP caller read a bare "killed <id>" and could not
// tell a closed pane from a row that was merely de-listed — the warning was
// written and then dropped on the floor.
func TestChatKillCarriesAWarningWrittenOnAZeroExit(t *testing.T) {
	const warning = "pfm chat kill: ses_live is live but carries no tmux address " +
		"(socket \"\" pane \"\") — recording the kill without closing it"
	service := &Service{backend: &backend{
		dispatch: func(_ context.Context, _ []string, stdout, stderr io.Writer) int {
			_, _ = io.WriteString(stdout, "killed ses_live\n")
			_, _ = io.WriteString(stderr, warning+"\n")
			return 0
		},
	}}

	_, output, err := service.chatKill(context.Background(), nil, KillInput{Target: "ses_live"})
	if err != nil {
		t.Fatalf("chatKill: %v", err)
	}
	if output.Status != "ok" {
		t.Fatalf("chatKill status = %q, want ok: the CLI exited 0", output.Status)
	}
	if !strings.Contains(output.Message, "killed ses_live") {
		t.Fatalf("chatKill message = %q, want the CLI's stdout", output.Message)
	}
	if !strings.Contains(output.Message, warning) {
		t.Fatalf("chatKill message = %q, want the stderr warning carried to the caller", output.Message)
	}
}

// The MCP caller's whole view of a kill is this Message. When the dispatched
// verb only de-listed a row — the answer an id gets when the fleet holds no
// live row for it — the mechanism must arrive with it. This is a PIN, not a
// regression: stdout always reached the caller; what changed is that the CLI
// now says which of the two it did (chat.KillOutcome), and MCP must not
// flatten that back into "killed <id>".
func TestChatKillMessageCarriesTheDeListedMechanism(t *testing.T) {
	service := &Service{backend: &backend{
		dispatch: func(_ context.Context, _ []string, stdout, _ io.Writer) int {
			_, _ = io.WriteString(stdout, "killed ses_cold\tde-listed only, no live pane closed\n")
			return 0
		},
	}}

	_, output, err := service.chatKill(context.Background(), nil, KillInput{Target: "ses_cold"})
	if err != nil {
		t.Fatalf("chatKill: %v", err)
	}
	if !strings.Contains(output.Message, "de-listed only, no live pane closed") {
		t.Fatalf("chatKill message = %q, want the de-listing named", output.Message)
	}
}

// TestChatKillDispatchesTargetAndExitFlag is the companion positive case:
// chat_kill{target, exit:true} reaches the CLI as `chat kill <target> --exit`
// unchanged, and a clean (verified) exit reports status ok.
func TestChatKillDispatchesTargetAndExitFlag(t *testing.T) {
	var calls [][]string
	service := &Service{backend: &backend{
		dispatch: func(_ context.Context, args []string, stdout, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			_, _ = io.WriteString(stdout, "killed my-chat\tclosing pane %3 on socket cx-1\n")
			return 0
		},
	}}

	_, output, err := service.chatKill(context.Background(), nil, KillInput{Target: "my-chat", Exit: true})
	if err != nil {
		t.Fatalf("chatKill: %v", err)
	}
	if output.Status != "ok" {
		t.Fatalf("chatKill status = %q, want ok", output.Status)
	}
	want := [][]string{{"chat", "kill", "my-chat", "--exit"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("dispatch calls = %q, want %q", calls, want)
	}
}
