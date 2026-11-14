package call

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TestCaptureScreenReadsOnlyTheScreen: kill's, reload's and status's checks
// parse CaptureScreen and CaptureStyled as the pane's screen (footer,
// composer, exit dialog), so a warning tmux writes to stderr must never join
// that text; a failed capture still names tmux's own complaint.
func TestCaptureScreenReadsOnlyTheScreen(t *testing.T) {
	warns := pfmtmux.WithClientRunner(context.Background(), testjail.TmuxRunner(deps.RunResult{
		Stdout: []byte("\u276f \n"), Stderr: []byte("tmux: terminal warning\n"),
	}))
	fails := pfmtmux.WithClientRunner(context.Background(), testjail.TmuxRunner(deps.RunResult{
		Stderr: []byte("can't find pane: %4\n"), ExitCode: 1,
	}))
	target := Target{SocketPath: "/sockets/cc-1", Pane: "%4"}
	engine, err := New(Dependencies{Tmux: TmuxCaller{}})
	if err != nil {
		t.Fatal(err)
	}
	for name, capture := range map[string]func(context.Context) (string, error){
		"plain":  func(ctx context.Context) (string, error) { return engine.CaptureScreen(ctx, target) },
		"styled": func(ctx context.Context) (string, error) { return engine.CaptureStyled(ctx, target) },
	} {
		screen, err := capture(warns)
		if err != nil || screen != "\u276f \n" {
			t.Fatalf("%s capture = %q, %v; want only the screen %q", name, screen, err, "\u276f \n")
		}
		_, err = capture(fails)
		if err == nil || !strings.Contains(err.Error(), "can't find pane: %4") {
			t.Fatalf("%s capture of a failing tmux = %v, want tmux's own complaint", name, err)
		}
	}
	if _, err := engine.CaptureScreen(warns, Target{Backend: "screen-x", SocketPath: "/s", Pane: "%1"}); err == nil {
		t.Fatal("CaptureScreen() of an unknown backend succeeded")
	}
}

// exitCaptureTmux fails every capture the way tmux's client exits, its reason on stderr.
type exitCaptureTmux struct {
	Tmux
	stderr string
}

func (fake exitCaptureTmux) Capture(context.Context, string, string, bool, int) (string, error) {
	return "", &exec.ExitError{Stderr: []byte(fake.stderr)}
}

// TestCaptureScreenNamesAGoneServer: a capture of a pane whose tmux server is
// gone wraps gather.ErrServerGone, the chat ended; any other failure keeps
// tmux's complaint and is not gone.
func TestCaptureScreenNamesAGoneServer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
		gone   bool
		want   string
	}{
		{
			"socket file gone", "error connecting to /sockets/cc-1 (No such file or directory)\n", true,
			"capture pane %4: no tmux server on socket: <nil>: error connecting to /sockets/cc-1 (No such file or directory)",
		},
		{
			"no server running", "no server running on /sockets/cc-1\n", true,
			"capture pane %4: no tmux server on socket: <nil>: no server running on /sockets/cc-1",
		},
		{"pane missing", "can't find pane: %4\n", false, "capture pane %4: <nil>: can't find pane: %4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := New(Dependencies{Tmux: exitCaptureTmux{stderr: tc.stderr}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = engine.CaptureScreen(context.Background(), Target{SocketPath: "/sockets/cc-1", Pane: "%4"})
			if err == nil || err.Error() != tc.want || errors.Is(err, gather.ErrServerGone) != tc.gone {
				t.Fatalf("CaptureScreen() = %v (gone %v), want %q (gone %v)",
					err, errors.Is(err, gather.ErrServerGone), tc.want, tc.gone)
			}
		})
	}
}

// TestLeaveCopyModeIsTheOneExit: a pane in copy mode is cancelled out of it, a
// pane outside it is left alone, a failed mode read is an error and nothing is
// sent, and a Herdr pane has no copy mode to leave.
func TestLeaveCopyModeIsTheOneExit(t *testing.T) {
	ctx := context.Background()
	target := Target{SocketPath: "/sockets/cc-1", Pane: "%4"}
	door := func(fake *fakeTmux) *Engine {
		engine, err := New(Dependencies{Tmux: fake})
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}
	inMode := &fakeTmux{inMode: true}
	if left, err := door(inMode).LeaveCopyMode(ctx, target); err != nil || !left || inMode.cancelModes != 1 {
		t.Fatalf("LeaveCopyMode() in copy mode = %t, %v, cancels %d; want true, nil, 1", left, err, inMode.cancelModes)
	}
	outside := &fakeTmux{}
	if left, err := door(outside).LeaveCopyMode(ctx, target); err != nil || left || outside.cancelModes != 0 {
		t.Fatalf(
			"LeaveCopyMode() outside copy mode = %t, %v, cancels %d; want false, nil, 0",
			left,
			err,
			outside.cancelModes,
		)
	}
	unread := &fakeTmux{inMode: true, modeErr: errors.New("server not answering")}
	if left, err := door(unread).LeaveCopyMode(ctx, target); err == nil || left || unread.cancelModes != 0 {
		t.Fatalf("LeaveCopyMode() over a failed mode read = %t, %v, cancels %d; want an error and no cancel",
			left, err, unread.cancelModes)
	}
	herdr := Target{Backend: backendHerdr, SocketPath: "/sockets/herdr.sock", Pane: "w1:p1"}
	if left, err := door(&fakeTmux{inMode: true}).LeaveCopyMode(ctx, herdr); err != nil || left {
		t.Fatalf("LeaveCopyMode() on Herdr = %t, %v; want false, nil", left, err)
	}
}
