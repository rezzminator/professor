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
