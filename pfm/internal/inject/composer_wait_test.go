package inject

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

// TestCodexInjectWaitsOutStartupSplash pins the 2026-10-04 launch race: five
// Codex seats injected in the second they launched had their brief typed into
// the startup splash and lost. Any screen without a held composer row — a
// blank pane, the session header, a boot status line, one frame that paints
// the glyph before the input is live — is waited out, and the message goes in
// only once the composer row holds.
func TestCodexInjectWaitsOutStartupSplash(t *testing.T) {
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
			result, err := engine.Inject(context.Background(), Request{
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

// TestCodexInjectOnEndlessSplashFailsNamingIt pins the bound: a pane whose
// composer never comes up is refused by name — the startup splash — with
// nothing typed, never a silent loss and never an empty result.
func TestCodexInjectOnEndlessSplashFailsNamingIt(t *testing.T) {
	fake := &splashTmux{
		fakeTmux: &fakeTmux{capture: codexIdleScreen, submitOnEnter: true},
		frames:   []string{codexHeaderSplash},
		endless:  true,
	}
	fake.paneCommand = "codex"
	engine := newSplashEngine(t, fake)
	result, err := engine.Inject(context.Background(), Request{
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
