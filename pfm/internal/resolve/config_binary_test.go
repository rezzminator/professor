package resolve

import (
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestResolverUsesConfiguredClaudeBasenameForSplitSessions(t *testing.T) {
	customClaude := "/opt/tools/claude enterprise"
	resolver, err := New(fakeTmux{}, Binaries{Values: map[pfmengine.ID]string{pfmengine.Claude: customClaude}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outcome := resolver.resolveSession("split", []ResolvedPane{
		{
			SocketPath:     "/jail/cc-configured",
			SessionName:    "split",
			PaneID:         "%1",
			CurrentCommand: customClaude,
		},
		{
			SocketPath:     "/jail/cc-configured",
			SessionName:    "split",
			PaneID:         "%2",
			CurrentCommand: "bash",
		},
	})
	if outcome.Code != 0 || outcome.Stdout != "/jail/cc-configured\t%1\n" {
		t.Fatalf("configured split resolution = %+v", outcome)
	}
}

func TestResolverUsesConfiguredCodexBasenameForWindowNames(t *testing.T) {
	customCodex := "/opt/tools/codex safe"
	resolver, err := New(fakeTmux{}, Binaries{Values: map[pfmengine.ID]string{pfmengine.Codex: customCodex}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outcome := resolver.resolveCxWindow("configured thread", []ResolvedPane{{
		SocketPath:     "/jail/custom-engine",
		PaneID:         "%9",
		CurrentCommand: customCodex,
		WindowName:     "configured thread",
	}})
	if outcome.Code != 0 || outcome.Stdout != "/jail/custom-engine\t%9\n" {
		t.Fatalf("configured Codex window resolution = %+v", outcome)
	}
}

func TestEngineCommandBasenamesAcceptConfiguredPaths(t *testing.T) {
	customClaude := "/opt/tools/claude enterprise"
	customCodex := "/opt/tools/codex safe"
	for _, test := range []struct {
		name    string
		command string
		check   func(string, ...string) bool
		binary  string
	}{
		{name: "claude", command: customClaude, check: isClaudePaneCommand, binary: customClaude},
		{name: "codex", command: customCodex, check: isCodexPaneCommand, binary: customCodex},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !test.check(test.command, test.binary) {
				t.Fatalf("configured command %q was not recognized", test.command)
			}
		})
	}
	if isCodexPaneCommand(customCodex) {
		t.Fatal("custom Codex basename was accepted without its configured policy")
	}
}

func TestResolverStillReturnsNoMatchForUnknownConfiguredCommand(t *testing.T) {
	resolver := &Resolver{tmux: fakeTmux{}}
	outcome := resolver.resolveSession("split", []ResolvedPane{
		{SocketPath: "/jail/cc", SessionName: "split", PaneID: "%1", CurrentCommand: "custom-claude"},
		{SocketPath: "/jail/cc", SessionName: "split", PaneID: "%2", CurrentCommand: "bash"},
	})
	if outcome.Code != 2 || !strings.Contains(outcome.Stderr, "0 running claude") {
		t.Fatalf("unknown command classification = %+v", outcome)
	}
}
