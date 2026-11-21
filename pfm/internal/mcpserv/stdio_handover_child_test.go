package mcpserv

import (
	"errors"
	"strings"
	"testing"
)

func TestStdioHandoverReplayForwardsWhatPrecedesTheInitializeAnswer(t *testing.T) {
	child := newHandoverChild("new", "notice-first", nil)
	runner := &handoverRunner{children: []*handoverChildScript{child}}
	handover, output, warnings, binary := newTestHandover(t, runner)
	handover.carry([]byte(handoverTestInitialize))
	installHandoverBuild(t, binary, "new build")
	if !handover.carry([]byte(handoverTestCall)) {
		t.Fatal("replaced image kept the tools/call")
	}
	want := `{"jsonrpc":"2.0","method":"notifications/message","params":{"data":"starting"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"result":{"image":"new"}}` + "\n"
	waitHandover(t, "the notice and the tools/call answer", func() bool { return output.String() == want })
	handover.closeInput()
	if err := handover.finish(); err != nil || warnings.String() != "" {
		t.Fatalf("finish = %v, warnings %q; want nil and none", err, warnings.String())
	}
}

func TestStdioHandoverFailureKeepsTheRunningImage(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		startErr     error
		wantWarning  string
		wantKilled   bool
	}{
		{
			name: "start refused", startErr: errors.New("exec format error"),
			wantWarning: `start ["{binary}" "mcp" "serve" "--stdio" "--family" "chat"]: exec format error`,
		},
		{
			name: "initialize answered with an error", answer: "error", wantKilled: true,
			wantWarning: `replay initialize: the installed pfm answered ` +
				`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"schema v8"}}`,
		},
		{
			name: "ended before it answered", answer: "eof", wantKilled: true,
			wantWarning: "replay initialize: the installed pfm ended before it answered",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := newHandoverChild("new", tc.answer, nil)
			runner := &handoverRunner{children: []*handoverChildScript{child}, startErr: tc.startErr}
			handover, output, warnings, binary := newTestHandover(t, runner)
			handover.carry([]byte(handoverTestInitialize))
			installHandoverBuild(t, binary, "new build")
			for _, frame := range []string{handoverTestCall, handoverTestCallAgain} {
				if handover.carry([]byte(frame)) {
					t.Fatalf("failed hand-over took %q from the running image", frame)
				}
			}
			want := "pfm mcp stdio: move this chat's MCP server onto the installed pfm " + binary +
				", keeping the running one: " + strings.ReplaceAll(tc.wantWarning, "{binary}", binary) + "\n"
			child.mutex.Lock()
			killed := child.killed
			child.mutex.Unlock()
			if warnings.String() != want || len(runner.starts) != 1 || killed != tc.wantKilled ||
				output.String() != "" {
				t.Fatalf("warnings %q, %d starts, killed %t, output %q; want %q, one start (the same build is "+
					"never retried), killed %t, no output", warnings.String(), len(runner.starts), killed,
					output.String(), want, tc.wantKilled)
			}
			if err := handover.finish(); err != nil {
				t.Fatalf("finish after a failed hand-over = %v, want nil", err)
			}
		})
	}
}
