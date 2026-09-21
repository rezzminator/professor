package mcpserv

import (
	"context"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/inject"
)

// fakeUnsignedInjector reproduces the exact refusal shape inject.Engine
// returns when it derived no sender identity of its own (internal/inject
// body.go refuseUnsigned) — the shared HTTP daemon's normal case, since that
// one process serves every chat on the machine and Claude Code attaches no
// per-call caller identity for it to sign with.
type fakeUnsignedInjector struct{}

func (fakeUnsignedInjector) Resolve(context.Context, string) (inject.Target, int, string, error) {
	return inject.Target{SocketPath: "cc-fixture", Pane: "%0"}, 0, "", nil
}

func (fakeUnsignedInjector) ResolveEngine(context.Context, string, string) (inject.Target, int, string, error) {
	return inject.Target{SocketPath: "cc-fixture", Pane: "%0"}, 0, "", nil
}

func (fakeUnsignedInjector) Capture(context.Context, string, int) (inject.Target, string, int, string, error) {
	return inject.Target{}, "", 0, "", nil
}

func (fakeUnsignedInjector) Inject(context.Context, inject.Request) (inject.Result, error) {
	return inject.Result{
		Status:     "refused",
		Code:       inject.CodeUndelivered,
		SocketPath: "cc-fixture",
		Pane:       "%0",
		Message: inject.ErrUnsigned.Error() + ": this process derived no identity of " +
			"its own, so the recipient would be asked to act on an instruction from " +
			"nobody. If this ran DETACHED (setsid/nohup/disowned), that is why: " +
			"detaching severs the process chain the handle is recovered from. Send " +
			"from the chat itself, state the identity (" + inject.SenderSessionEnv +
			"=$(pfm whoami) " + inject.SenderLabelEnv + "=<label> <command>), or " +
			"pass --allow-unsigned to send it anyway",
	}, nil
}

func (fakeUnsignedInjector) ScheduleAfterCurrentTurn(
	context.Context, inject.Request,
) (inject.Result, error) {
	return inject.Result{}, nil
}

func (fakeUnsignedInjector) ScheduleSelfCompact(
	context.Context, string, []string,
) (inject.Result, error) {
	return inject.Result{}, nil
}

// TestMCPUnsignedRefusalNamesAnMCPReachableRemedy pins the actual field
// failure: nine chat_inject calls over the shared HTTP daemon, an explicit
// (non-self) target, every one refused because the daemon derived no sender
// of its own. inject/body.go's wording tells the reader to set an
// environment variable or pass --allow-unsigned — neither reachable from an
// MCP tool call — and blames a detached process chain that never happened
// here. The MCP surface must replace that remedy with one the caller can
// actually act on, and must drop the impossible instructions entirely.
func TestMCPUnsignedRefusalNamesAnMCPReachableRemedy(t *testing.T) {
	service := newService("test", &backend{
		injector:             fakeUnsignedInjector{},
		allowAmbientIdentity: false,
	})
	_, output, err := service.chatInject(
		context.Background(),
		nil,
		InjectInput{Target: "some-explicit-session", Message: "hello"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.Message, inject.ErrUnsigned.Error()) {
		t.Fatalf("chat_inject message dropped the refusal cause: %q", output.Message)
	}
	if strings.Contains(output.Message, "--allow-unsigned") ||
		strings.Contains(output.Message, inject.SenderSessionEnv+"=$(pfm whoami)") ||
		strings.Contains(output.Message, "DETACHED (setsid/nohup/disowned)") {
		t.Fatalf(
			"chat_inject message still tells an MCP caller to do something unreachable from MCP: %q",
			output.Message,
		)
	}
	if !strings.Contains(output.Message, "pfm chat") {
		t.Fatalf("chat_inject message names no MCP-reachable remedy: %q", output.Message)
	}
}

// TestMCPSelfRefusalNamesAnMCPReachableRemedy covers the earlier self-target
// refusal path (chat_whoami with no _meta.threadId, ambient identity
// disabled): the config-fact wording the daemon used to return told the
// reader nothing they could do about it.
func TestMCPSelfRefusalNamesAnMCPReachableRemedy(t *testing.T) {
	service := newService("test", &backend{allowAmbientIdentity: false})
	_, output, err := service.chatWhoami(context.Background(), nil, WhoamiInput{})
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "not_found" {
		t.Fatalf("chat_whoami status = %q, want not_found", output.Status)
	}
	if !strings.Contains(output.Message, "pfm chat") {
		t.Fatalf("chat_whoami message names no MCP-reachable remedy: %q", output.Message)
	}
}
