package call

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestCaptureScreenReadsOnlyTheScreen: kill's, reload's and status's checks
// parse CaptureScreen and CaptureStyled as the pane's screen (footer,
// composer, exit dialog), so a warning tmux writes to stderr must never join
// that text; a failed capture still names tmux's own complaint.
func TestCaptureScreenReadsOnlyTheScreen(t *testing.T) {
	directory := t.TempDir()
	warns := filepath.Join(directory, "tmux-warns")
	script := "#!/bin/sh\nprintf '\\342\\235\\257 \\n'\necho 'tmux: terminal warning' >&2\nexit 0\n"
	if err := testjail.WriteExecutable(warns, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	fails := filepath.Join(directory, "tmux-fails")
	failing := []byte("#!/bin/sh\necho \"can't find pane: %4\" >&2\nexit 1\n")
	if err := testjail.WriteExecutable(fails, failing, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	target := Target{SocketPath: "/sockets/cc-1", Pane: "%4"}
	door := func(binary string) *Engine {
		engine, err := New(Dependencies{Tmux: TmuxCaller{Binary: binary}})
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}
	for name, capture := range map[string]func(*Engine) (string, error){
		"plain":  func(engine *Engine) (string, error) { return engine.CaptureScreen(ctx, target) },
		"styled": func(engine *Engine) (string, error) { return engine.CaptureStyled(ctx, target) },
	} {
		screen, err := capture(door(warns))
		if err != nil || screen != "\u276f \n" {
			t.Fatalf("%s capture = %q, %v; want only the screen %q", name, screen, err, "\u276f \n")
		}
		_, err = capture(door(fails))
		if err == nil || !strings.Contains(err.Error(), "can't find pane: %4") {
			t.Fatalf("%s capture of a failing tmux = %v, want tmux's own complaint", name, err)
		}
	}
	if _, err := door(warns).CaptureScreen(ctx, Target{Backend: "screen-x", SocketPath: "/s", Pane: "%1"}); err == nil {
		t.Fatal("CaptureScreen() of an unknown backend succeeded")
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
