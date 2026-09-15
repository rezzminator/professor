package inject

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

func TestPaneCommandEngineAcceptsConfiguredBinaryBasenames(t *testing.T) {
	customClaude := "/opt/tools/claude enterprise"
	customCodex := "/opt/tools/codex safe"
	binaries := map[pfmengine.ID]string{pfmengine.Claude: customClaude, pfmengine.Codex: customCodex}
	if got := paneCommandEngine(customClaude, binaries); got != string(pfmengine.Claude) {
		t.Fatalf("configured Claude command classified as %q", got)
	}
	if got := paneCommandEngine(customCodex, binaries); got != string(pfmengine.Codex) {
		t.Fatalf("configured Codex command classified as %q", got)
	}
	if got := paneCommandEngine("custom-codex", binaries); got != "" {
		t.Fatalf("unknown command classified as %q", got)
	}
}

func TestInjectUsesConfiguredBinaryToDescribeBusyPane(t *testing.T) {
	customCodex := "/opt/tools/codex safe"
	fake := &fakeTmux{
		capture:       "Working (2s · 9 tokens) · esc to interrupt\n› ",
		paneCommand:   customCodex,
		submitOnEnter: true,
		submitCapture: "Working (2s · 9 tokens)\nPending message\n{{MESSAGE}}\n› ",
	}
	t.Setenv("PFM_HOME", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv("CHAT_INJECT_SOCKET", "")
	engine, err := New(Dependencies{
		Resolver: fakeResolver{
			socket: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-configured"),
			target: "%1",
		},
		Tmux:         fake,
		ClaudeBinary: "/opt/tools/claude enterprise",
		CodexBinary:  customCodex,
		Options: Options{
			Poll:             time.Nanosecond,
			EnterSettle:      time.Nanosecond,
			ProofSettle:      time.Nanosecond,
			BusyTries:        2,
			InterruptTries:   2,
			StashTries:       2,
			SettleTries:      2,
			EnterTries:       2,
			LockTimeout:      time.Second,
			LockPoll:         time.Nanosecond,
			LockMaxHold:      time.Second,
			LockRoot:         t.TempDir(),
			ThenLogRoot:      t.TempDir(),
			Sender:           &Sender{Session: "sender"},
			ClaudeInlineMax:  ClaudeInlineMax,
			CodexInlineMax:   CodexInlineMax,
			DisableSignature: true,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := engine.Inject(context.Background(), Request{
		Target:  "configured",
		Message: "queue this",
	})
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if result.Code != 0 || result.Status != "queued" || !strings.Contains(result.Message, "busy Codex") {
		t.Fatalf("configured Codex injection = %+v", result)
	}
}
