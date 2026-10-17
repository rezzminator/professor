package mcpserv

import (
	"context"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

// fakeUnsignedCaller reproduces the exact refusal shape pfmcall.Engine
// returns when it derived no sender identity of its own (internal/call
// body.go refuseUnsigned) — the shared HTTP daemon's normal case, since that
// one process serves every chat on the machine and Claude Code attaches no
// per-call caller identity for it to sign with.
type fakeUnsignedCaller struct{}

func (fakeUnsignedCaller) Resolve(context.Context, string) (pfmcall.Target, int, string, error) {
	return pfmcall.Target{SocketPath: "cc-fixture", Pane: "%0"}, 0, "", nil
}

func (fakeUnsignedCaller) ResolveEngine(context.Context, string, string) (pfmcall.Target, int, string, error) {
	return pfmcall.Target{SocketPath: "cc-fixture", Pane: "%0"}, 0, "", nil
}

func (fakeUnsignedCaller) Capture(context.Context, string, int) (pfmcall.Target, string, int, string, error) {
	return pfmcall.Target{}, "", 0, "", nil
}

func (fakeUnsignedCaller) Call(context.Context, pfmcall.Request) (pfmcall.Result, error) {
	return pfmcall.Result{
		Status:     "refused",
		Code:       pfmcall.CodeUndelivered,
		SocketPath: "cc-fixture",
		Pane:       "%0",
		Message: pfmcall.ErrUnsigned.Error() + ": this process derived no identity of " +
			"its own, so the recipient would be asked to act on an instruction from " +
			"nobody. If this ran DETACHED (setsid/nohup/disowned), that is why: " +
			"detaching severs the process chain the handle is recovered from. Send " +
			"from the chat itself, state the identity (" + pfmcall.SenderSessionEnv +
			"=$(pfm whoami) " + pfmcall.SenderLabelEnv + "=<label> <command>), or " +
			"pass --allow-unsigned to send it anyway",
	}, nil
}

// TestMCPUnsignedRefusalNamesAnMCPReachableRemedy pins the actual field
// failure: nine `call` tool calls over the shared HTTP daemon, an explicit
// (non-self) target, every one refused because the daemon derived no sender
// of its own. call/body.go's wording tells the reader to set an
// environment variable or pass --allow-unsigned — neither reachable from an
// MCP tool call — and blames a detached process chain that never happened
// here. The MCP surface must replace that remedy with one the caller can
// actually act on, and must drop the impossible instructions entirely.
func TestMCPUnsignedRefusalNamesAnMCPReachableRemedy(t *testing.T) {
	t.Parallel()
	service := newService("test", &backend{
		callEngine:           fakeUnsignedCaller{},
		allowAmbientIdentity: false,
	})
	_, output, err := service.chatCall(
		context.Background(),
		nil,
		CallInput{Target: "some-explicit-session", Message: "hello"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.Message, pfmcall.ErrUnsigned.Error()) {
		t.Fatalf("call message dropped the refusal cause: %q", output.Message)
	}
	if strings.Contains(output.Message, "--allow-unsigned") ||
		strings.Contains(output.Message, pfmcall.SenderSessionEnv+"=$(pfm whoami)") ||
		strings.Contains(output.Message, "DETACHED (setsid/nohup/disowned)") {
		t.Fatalf(
			"call message still tells an MCP caller to do something unreachable from MCP: %q",
			output.Message,
		)
	}
	if !strings.Contains(output.Message, "pfm chat") {
		t.Fatalf("call message names no MCP-reachable remedy: %q", output.Message)
	}
}

// TestMCPSelfRefusalNamesAnMCPReachableRemedy covers the earlier self-target
// refusal path (whoami with no _meta.threadId, ambient identity
// disabled): the config-fact wording the daemon used to return told the
// reader nothing they could do about it.
func TestMCPSelfRefusalNamesAnMCPReachableRemedy(t *testing.T) {
	t.Parallel()
	service := newService("test", &backend{allowAmbientIdentity: false})
	_, output, err := service.chatWhoami(context.Background(), nil, WhoamiInput{})
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "not_found" {
		t.Fatalf("whoami status = %q, want not_found", output.Status)
	}
	if !strings.Contains(output.Message, "pfm chat") {
		t.Fatalf("whoami message names no MCP-reachable remedy: %q", output.Message)
	}
}
