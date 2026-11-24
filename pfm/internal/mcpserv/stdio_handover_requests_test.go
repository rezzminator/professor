package mcpserv

import (
	"slices"
	"testing"
)

const (
	handoverTestHold   = `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"hold"}}` + "\n"
	handoverTestCancel = `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5}}` + "\n"
)

// TestStdioHandoverCarriesACancellationToTheProcessHoldingItsRequest: a
// cancelled request goes to the process that took it, never to the one serving
// now, and a retired child it held then ends its input.
func TestStdioHandoverCarriesACancellationToTheProcessHoldingItsRequest(t *testing.T) {
	probeClock(t)
	t.Run("a retired installed pfm", func(t *testing.T) {
		first, second := newHandoverChild("first", "result", nil), newHandoverChild("second", "result", nil)
		runner := &handoverRunner{children: []*handoverChildScript{first, second}}
		handover, _, warnings, binary := newTestHandover(t, runner)
		handover.carry([]byte(handoverTestInitialize))
		installHandoverBuild(t, binary, "first build")
		handover.carry([]byte(handoverTestHold))
		installHandoverBuild(t, binary, "second build, longer")
		handover.carry([]byte(handoverTestCallAgain))
		if !handover.carry([]byte(handoverTestCancel)) {
			t.Fatal("handed-over server kept the cancellation")
		}
		waitHandover(t, "the first installed pfm's input to end", func() bool {
			select {
			case <-first.done:
				return true
			default:
				return false
			}
		})
		wantFirst := []string{handoverTestInitialize, handoverTestHold, handoverTestCancel}
		wantSecond := []string{handoverTestInitialize, handoverTestCallAgain}
		// A carried frame returns once the child's reader took its bytes, before the child records the line.
		waitHandover(t, "the second installed pfm to record the call carried to it", func() bool {
			return len(second.frames()) >= len(wantSecond)
		})
		if got := first.frames(); !slices.Equal(got, wantFirst) {
			t.Fatalf("first installed pfm received %q, want %q", got, wantFirst)
		}
		if got := second.frames(); !slices.Equal(got, wantSecond) {
			t.Fatalf("second installed pfm received %q, want %q", got, wantSecond)
		}
		handover.closeInput()
		if err := handover.finish(); err != nil || warnings.String() != "" {
			t.Fatalf("finish = %v, warnings %q; want nil and none", err, warnings.String())
		}
	})
	t.Run("the image the server started on", func(t *testing.T) {
		child := newHandoverChild("new", "result", nil)
		runner := &handoverRunner{children: []*handoverChildScript{child}}
		handover, _, _, binary := newTestHandover(t, runner)
		handover.carry([]byte(handoverTestInitialize))
		if handover.carry([]byte(handoverTestHold)) {
			t.Fatal("untouched image handed the call over")
		}
		installHandoverBuild(t, binary, "new build")
		if handover.carry([]byte(handoverTestCancel)) {
			t.Fatal("the cancellation of the running image's call went to the installed pfm")
		}
		handover.closeInput()
		if err := handover.finish(); err != nil {
			t.Fatalf("finish = %v, want nil", err)
		}
		if got, want := child.frames(), []string{handoverTestInitialize}; !slices.Equal(got, want) {
			t.Fatalf("installed pfm received %q, want %q", got, want)
		}
	})
}

// TestStdioHandoverAnswersARequestTheInstalledPfmCouldNotTake: a request whose
// write to the child fails is answered with an error, so the client never waits.
func TestStdioHandoverAnswersARequestTheInstalledPfmCouldNotTake(t *testing.T) {
	probeClock(t)
	child := newHandoverChild("new", "result", nil)
	runner := &handoverRunner{children: []*handoverChildScript{child}}
	handover, output, _, binary := newTestHandover(t, runner)
	handover.carry([]byte(handoverTestInitialize))
	installHandoverBuild(t, binary, "new build")
	handover.carry([]byte(handoverTestCall))
	firstReply := `{"jsonrpc":"2.0","id":2,"result":{"image":"new"}}` + "\n"
	waitHandover(t, "the installed pfm's answer", func() bool { return output.String() == firstReply })
	if err := child.Kill(); err != nil {
		t.Fatal(err)
	}
	<-child.done
	handover.carry([]byte(handoverTestCallAgain))
	want := firstReply + `{"jsonrpc":"2.0","id":3,"error":{"code":-32603,"message":"the installed pfm ` + binary +
		` did not take the request: io: read/write on closed pipe"}}` + "\n"
	if got := output.String(); got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
