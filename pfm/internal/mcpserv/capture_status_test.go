package mcpserv

import (
	"context"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

// captureCodeCaller answers Capture with one pinned engine code and detail,
// the way pfmcall.Engine.Capture does for a resolved-but-unreadable pane
// (CodeDead), a target that matched nothing (CodeUnknown), several matches
// (CodeAmbiguous), or any other failure the engine grows later.
type captureCodeCaller struct {
	code   int
	detail string
}

func (fake captureCodeCaller) Resolve(context.Context, string) (pfmcall.Target, int, string, error) {
	return pfmcall.Target{SocketPath: "cc-fixture", Pane: "%0"}, fake.code, fake.detail, nil
}

func (fake captureCodeCaller) ResolveEngine(
	context.Context, string, string,
) (pfmcall.Target, int, string, error) {
	return pfmcall.Target{SocketPath: "cc-fixture", Pane: "%0"}, fake.code, fake.detail, nil
}

func (fake captureCodeCaller) Capture(
	context.Context, string, int,
) (pfmcall.Target, string, int, string, error) {
	return pfmcall.Target{SocketPath: "cc-fixture", Pane: "%0"}, "", fake.code, fake.detail, nil
}

func (captureCodeCaller) Call(context.Context, pfmcall.Request) (pfmcall.Result, error) {
	return pfmcall.Result{}, nil
}

// TestChatCaptureAnswersADeadPaneDistinctlyFromNotFound pins the honesty
// split chatOpenDetached already makes in this package: "no such chat"
// (statusNotFound) is reserved for a target that matched nothing. A pane that
// resolved and then could not be READ — pfmcall.CodeDead, which
// pfmcall.Engine.Capture returns for every tmux capture I/O error — and any
// other failed capture must answer something else, or a live chat reads as
// absent during one transient tmux read.
func TestChatCaptureAnswersADeadPaneDistinctlyFromNotFound(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		code   int
		detail string
		want   string
	}{
		{"resolved pane could not be read", pfmcall.CodeDead, "target pane is dead or unreadable", statusDead},
		{"target matched no live chat", pfmcall.CodeUnknown, `target "gone" matched no live chat`, statusNotFound},
		{"several targets matched", pfmcall.CodeAmbiguous, "two rows match", statusAmbiguous},
		{"any other capture failure", 99, "capture failed for a new reason", statusError},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newService("test", &backend{
				callEngine:           captureCodeCaller{code: test.code, detail: test.detail},
				allowAmbientIdentity: true,
			})
			_, output, err := service.chatCapture(context.Background(), nil, CaptureInput{Target: "some-chat"})
			if err != nil {
				t.Fatalf("capture: %v", err)
			}
			if output.Status != test.want {
				t.Fatalf(
					"capture status for engine code %d = %q, want %q — %q must never read as %q",
					test.code, output.Status, test.want, test.detail, statusNotFound,
				)
			}
			if output.Code != test.code {
				t.Fatalf("capture code = %d, want the engine's own %d", output.Code, test.code)
			}
			if output.Message != test.detail {
				t.Fatalf("capture message = %q, want the engine's detail %q", output.Message, test.detail)
			}
		})
	}
}

// TestChatCaptureSurfacesCodeCaptureFailedAsAToolError pins
// pfmcall.CodeCaptureFailed's own arm: unlike CodeDead/CodeAmbiguous/
// CodeUnknown, which describe the TARGET pane and stay legitimate soft
// answers (err stays nil so a caller reads them off Status), CodeCaptureFailed
// means the probe itself never ran (pfmtmux.CouldNotRun) — this tool's own
// execution failed, so it must surface as a real Go error the way
// chatOpenDetached and cliAction (actions.go) already return one for their
// statusError outcomes, not just a JSON status field a caller can miss.
func TestChatCaptureSurfacesCodeCaptureFailedAsAToolError(t *testing.T) {
	t.Parallel()
	service := newService("test", &backend{
		callEngine: captureCodeCaller{
			code:   pfmcall.CodeCaptureFailed,
			detail: "could not run tmux to capture %0: exec: \"tmux\": executable file not found in $PATH",
		},
		allowAmbientIdentity: true,
	})
	_, output, err := service.chatCapture(context.Background(), nil, CaptureInput{Target: "some-chat"})
	if err == nil {
		t.Fatalf(
			"capture on pfmcall.CodeCaptureFailed returned a nil error — a probe that could not run must surface as a real tool error, not silently as a status field",
		)
	}
	if output.Status != statusError || output.Code != pfmcall.CodeCaptureFailed {
		t.Fatalf("capture output = %+v, want Status=%q Code=%d", output, statusError, pfmcall.CodeCaptureFailed)
	}
}

// TestChatCaptureKeepsOKForASuccessfulCapture is the control arm: the split
// above must not turn a healthy capture into a failure status.
func TestChatCaptureKeepsOKForASuccessfulCapture(t *testing.T) {
	t.Parallel()
	service := newService("test", &backend{
		callEngine:           captureCodeCaller{code: 0},
		allowAmbientIdentity: true,
	})
	_, output, err := service.chatCapture(context.Background(), nil, CaptureInput{Target: "some-chat"})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if output.Status != "ok" || output.Code != 0 {
		t.Fatalf("capture on a healthy pane = %+v, want ok/0", output)
	}
}
