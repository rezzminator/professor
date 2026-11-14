package call

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// splashTmux plays a Codex pane that is still booting: every capture before
// the composer comes up returns the next pre-composer frame (the last one
// forever when endless), and only then does the ordinary fake take over. It
// records every key, literal or paste that reached the pane while the last
// screen it showed was still a boot frame — the keystrokes a booting TUI
// swallows.
type splashTmux struct {
	*fakeTmux
	mu      sync.Mutex
	frames  []string
	endless bool
	served  int
	booting bool
	early   []string
}

func (fake *splashTmux) Capture(
	ctx context.Context,
	socket, target string,
	styled bool,
	scrollback int,
) (string, error) {
	fake.mu.Lock()
	if fake.endless || fake.served < len(fake.frames) {
		frame := fake.frames[min(fake.served, len(fake.frames)-1)]
		fake.served++
		fake.booting = true
		fake.mu.Unlock()
		return frame, nil
	}
	fake.booting = false
	fake.mu.Unlock()
	return fake.fakeTmux.Capture(ctx, socket, target, styled, scrollback)
}

func (fake *splashTmux) noteInput(what string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.booting {
		fake.early = append(fake.early, what)
	}
}

func (fake *splashTmux) SendLiteral(ctx context.Context, socket, target, text string) error {
	fake.noteInput("literal " + text)
	return fake.fakeTmux.SendLiteral(ctx, socket, target, text)
}

func (fake *splashTmux) SendPaste(ctx context.Context, socket, target, text string) error {
	fake.noteInput("paste")
	return fake.fakeTmux.SendPaste(ctx, socket, target, text)
}

func (fake *splashTmux) SendKey(ctx context.Context, socket, target, key string) error {
	fake.noteInput("key " + key)
	return fake.fakeTmux.SendKey(ctx, socket, target, key)
}

const (
	codexIdleScreen   = "› \n\n  gpt-6.1-sol high · ~/work"
	codexHeaderSplash = "╭──────────────────────────────╮\n" +
		"│ >_ OpenAI Codex (v0.149.0)   │\n" +
		"│ model:     gpt-6.1-sol high  │\n" +
		"│ directory: ~/work            │\n" +
		"╰──────────────────────────────╯"
)

func newSplashEngine(t *testing.T, fake *splashTmux) *Engine {
	t.Helper()
	engine := newTestEngine(t, "cx-splash", fake.fakeTmux)
	engine.tmux = fake
	engine.options.ComposerWait = 50 * time.Millisecond
	return engine
}

// TestCodexCallWaitsOutStartupSplash pins the 2026-10-04 launch race: five
// Codex seats called in the second they launched had their brief typed into
// the startup splash and lost. Any screen without a held composer row — a
// blank pane, the session header, a boot status line, one frame that paints
// the glyph before the input is live — is waited out, and the message goes in
// only once the composer row holds.
func TestCodexCallWaitsOutStartupSplash(t *testing.T) {
	tests := []struct {
		name   string
		frames []string
	}{
		{name: "blank pane", frames: []string{"", "", ""}},
		{name: "session header", frames: []string{codexHeaderSplash, codexHeaderSplash}},
		{name: "boot status line", frames: []string{
			codexHeaderSplash + "\n\n• Starting MCP servers (0/2): professor",
		}},
		{name: "glyph before the input is live", frames: []string{
			"› ", codexHeaderSplash,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &splashTmux{
				fakeTmux: &fakeTmux{capture: codexIdleScreen, submitOnEnter: true},
				frames:   test.frames,
			}
			fake.paneCommand = "codex"
			engine := newSplashEngine(t, fake)
			engine.options.ComposerWait = 5 * time.Second
			result, err := engine.Call(context.Background(), Request{
				Target:  "chat",
				Message: "the round-1 brief",
			})
			if err != nil || result.Code != 0 || !result.Typed {
				t.Fatalf("delivery after the splash: result=%+v err=%v", result, err)
			}
			if len(fake.early) != 0 {
				t.Fatalf("input reached the pane while it was still booting: %q", fake.early)
			}
		})
	}
}

// TestCodexCallOnEndlessSplashFailsNamingIt pins the bound: a pane whose
// composer never comes up is refused by name — the startup splash — with
// nothing typed, never a silent loss and never an empty result.
func TestCodexCallOnEndlessSplashFailsNamingIt(t *testing.T) {
	fake := &splashTmux{
		fakeTmux: &fakeTmux{capture: codexIdleScreen, submitOnEnter: true},
		frames:   []string{codexHeaderSplash},
		endless:  true,
	}
	fake.paneCommand = "codex"
	engine := newSplashEngine(t, fake)
	result, err := engine.Call(context.Background(), Request{
		Target:  "chat",
		Message: "the round-1 brief",
	})
	if err != nil {
		t.Fatalf("an endless splash is a refusal, not an engine error: %v", err)
	}
	if result.Code != CodeUndelivered || result.Typed {
		t.Fatalf("result=%+v, want an undelivered refusal with nothing typed", result)
	}
	if !strings.Contains(result.Message, "startup splash") {
		t.Fatalf("message %q does not name the startup splash", result.Message)
	}
	if len(fake.early) != 0 {
		t.Fatalf("input reached a pane that never finished booting: %q", fake.early)
	}
}

// TestAwaitComposerRowTellsAFailedReadFromTheSplash pins the verdicts a
// reuser (reload --then, spawn) depends on: a read that failed is its own
// error, never the splash timeout and never a capture that reads as ready.
func TestAwaitComposerRowTellsAFailedReadFromTheSplash(t *testing.T) {
	dead := errors.New("can't find pane %1")
	capture, err := AwaitComposerRow(context.Background(), ComposerWait{
		Read: func(context.Context) (string, error) { return "", dead },
		Poll: time.Nanosecond,
	})
	if !errors.Is(err, dead) || errors.Is(err, ErrStartupSplash) || capture != "" {
		t.Fatalf("failed read: capture=%q err=%v, want the read's own error", capture, err)
	}
	capture, err = AwaitComposerRow(context.Background(), ComposerWait{
		Read:  func(context.Context) (string, error) { return codexHeaderSplash, nil },
		Bound: time.Millisecond,
		Poll:  time.Nanosecond,
	})
	if !errors.Is(err, ErrStartupSplash) || capture != codexHeaderSplash {
		t.Fatalf("endless splash: capture=%q err=%v, want ErrStartupSplash with the last screen", capture, err)
	}
}

// slowEchoTmux plays a pane still drawing its last turn: a typed message
// reaches the screen only after hold more captures, and an Enter pressed
// before it shows is swallowed — the TUI has not read the text it would
// submit. With cut, each held frame is caught mid-repaint: the transcript
// without the composer row.
type slowEchoTmux struct {
	*fakeTmux
	mu     sync.Mutex
	hold   int
	unseen int
	before string
	cut    bool
}

func (fake *slowEchoTmux) Capture(
	ctx context.Context,
	socket, target string,
	styled bool,
	scrollback int,
) (string, error) {
	fake.mu.Lock()
	if fake.unseen > 0 {
		fake.unseen--
		before := fake.before
		cut := fake.cut
		fake.mu.Unlock()
		if lines := strings.Split(before, "\n"); cut && lastComposerRow(lines) >= 0 {
			before = strings.Join(lines[:lastComposerRow(lines)], "\n")
		}
		return before, nil
	}
	fake.mu.Unlock()
	return fake.fakeTmux.Capture(ctx, socket, target, styled, scrollback)
}

func (fake *slowEchoTmux) SendLiteral(ctx context.Context, socket, target, text string) error {
	before, err := fake.fakeTmux.Capture(ctx, socket, target, false, 0)
	if err != nil {
		return err
	}
	fake.mu.Lock()
	fake.before, fake.unseen = before, fake.hold
	fake.mu.Unlock()
	return fake.fakeTmux.SendLiteral(ctx, socket, target, text)
}

func (fake *slowEchoTmux) SendKey(ctx context.Context, socket, target, key string) error {
	fake.mu.Lock()
	swallowed := key == "Enter" && fake.unseen > 0
	fake.mu.Unlock()
	if !swallowed {
		return fake.fakeTmux.SendKey(ctx, socket, target, key)
	}
	fake.fakeTmux.mu.Lock()
	defer fake.fakeTmux.mu.Unlock()
	fake.keys = append(fake.keys, key)
	return nil
}

// TestRenderSettleReadsTheComposerNotTheTranscript pins the held call that a
// pane still drawing lost: every call from one sender ends in the same
// signature, so the previous call's transcript row read as this message's
// echo, Enter went out before the pane showed the text, and the delivery
// proof found the message still in the composer. The settle reads the
// composer row and below, so the one Enter waits for the echo; a frame caught
// mid-repaint, with no composer row, holds no echo either.
func TestRenderSettleReadsTheComposerNotTheTranscript(t *testing.T) {
	for _, row := range []struct {
		name string
		cut  bool
	}{
		{"held frames show the composer", false},
		{"held frames are cut above the composer", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			fake := &slowEchoTmux{fakeTmux: &fakeTmux{capture: "❯ ", submitOnEnter: true}}
			engine := newTestEngine(t, "slow-echo", fake.fakeTmux)
			engine.tmux = fake
			first, err := engine.Call(context.Background(), Request{Target: "chat", Message: "first call"})
			if err != nil || first.Code != 0 {
				t.Fatalf("first call: result=%+v err=%v", first, err)
			}
			fake.fakeTmux.mu.Lock()
			transcript := fake.capture
			fake.keys = nil
			fake.fakeTmux.mu.Unlock()
			fake.mu.Lock()
			fake.hold, fake.cut = 2, row.cut
			fake.mu.Unlock()
			engine.options.SettleTries = 4
			second, err := engine.Call(context.Background(), Request{Target: "chat", Message: "second call"})
			if err != nil || second.Code != 0 || second.Status != "delivered" {
				t.Fatalf("second call: result=%+v err=%v", second, err)
			}
			fake.fakeTmux.mu.Lock()
			keys := strings.Join(fake.keys, ",")
			fake.fakeTmux.mu.Unlock()
			if keys != "C-s,Enter" {
				t.Fatalf(
					"keys = %q, want the draft guard then one Enter; screen before the message: %q",
					keys,
					transcript,
				)
			}
		})
	}
}

// repaintTmux plays a pane read while it repaints: the visible screen is whole
// on every read, and the scrollback still holds an older frame.
type repaintTmux struct {
	*fakeTmux
	whole, older string
	visible      int
}

func (fake *repaintTmux) Capture(_ context.Context, _, _ string, _ bool, scrollback int) (string, error) {
	if scrollback > 0 {
		return fake.older, nil
	}
	fake.visible++
	return fake.whole, nil
}

// TestProofComposerReadsARepaintingFrameAgain pins the held call a repainting
// pane failed: the proof capture ended inside a transcript row, above the
// composer, and the scrollback's older frame still showed the composer holding
// the message, so a delivered call was reported as PROOF-CONTRADICTION.
func TestProofComposerReadsARepaintingFrameAgain(t *testing.T) {
	const cut = "> first call\n⏺ answer\n> a long row the read cut he"
	for _, row := range []struct {
		name, proof, whole, line, from string
		visible                        int
	}{
		{"composer in the proof", "⏺ answer\n❯ ", "", "❯ ", "⏺ answer\n❯ ", 0},
		{"cut frame, whole on the next read", cut, "⏺ held\n❯ ", "❯ ", "⏺ held\n❯ ", 1},
		{"no composer on either read", cut, cut, "❯ second call", cut, 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			fake := &repaintTmux{fakeTmux: &fakeTmux{}, whole: row.whole, older: "❯ second call\n" + cut}
			engine := newTestEngine(t, "repaint", fake.fakeTmux)
			engine.tmux = fake
			engine.options.Poll = time.Millisecond
			line, from := engine.proofComposer(context.Background(), Target{Pane: "%1"}, row.proof)
			if line != row.line || from != row.from || fake.visible != row.visible {
				t.Fatalf("proofComposer(%q) = %q from %q after %d visible read(s); want %q from %q after %d",
					row.proof, line, from, fake.visible, row.line, row.from, row.visible)
			}
		})
	}
}
