package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestMCPRuntimeBindsTheVerbLayerToTheCommandsRuntime pins the one bridge into
// MCP: the typed verbs and the roster rung both run on the command's own
// runtime — the same one, never a second load — and the argv dispatcher left
// for the stateful verbs refuses anything but chat argv.
func TestMCPRuntimeBindsTheVerbLayerToTheCommandsRuntime(t *testing.T) {
	runtime := commandRuntime{Paths: paths.Values{TmuxDir: "/jail/tmux"}}
	runtime.Config.MCP.HTTP.Port = 43117
	bridged := mcpRuntime(runtime, false)
	if bridged.DaemonAddress != "127.0.0.1:43117" {
		t.Fatalf("DaemonAddress = %q, want selected command runtime port", bridged.DaemonAddress)
	}
	verbs, ok := bridged.Chat.(pfmchat.Verbs)
	if !ok || verbs.Runtime == nil || verbs.Runtime.Paths.TmuxDir != "/jail/tmux" {
		t.Fatalf("Chat = %#v, want chat.Verbs over the command's runtime", bridged.Chat)
	}
	names, ok := bridged.Names.(pfmchat.NameResolver)
	if !ok || names.Runtime != verbs.Runtime {
		t.Fatalf("Names = %#v, want chat.NameResolver over the verbs' own runtime", bridged.Names)
	}
	var stderr strings.Builder
	if code := bridged.Dispatch(context.Background(), []string{"ls"}, io.Discard, &stderr); code != 2 ||
		!strings.Contains(stderr.String(), "requires chat argv") {
		t.Fatalf("Dispatch(ls) = %d %q, want the non-chat argv refused", code, stderr.String())
	}
	if bridged.AllowAmbientIdentity || !mcpRuntime(commandRuntime{}, true).AllowAmbientIdentity {
		t.Fatal(
			"AllowAmbientIdentity must follow ambient: the shared daemon fails closed, stdio runs inside its caller",
		)
	}
}

// TestMCPDispatchReadsTheConfigAtSpawnTime: the long-lived MCP daemon loaded
// its runtime once at start, yet every chat verb it dispatches reads the
// machine config as the file holds it at that moment (config.Runtime.Reload):
// an edit that breaks the file refuses the spawn as the CLI would, never
// launching on the stale copy.
func TestMCPDispatchReadsTheConfigAtSpawnTime(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"version":2}`)
	machine, err := config.Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{Config: machine, Paths: paths.Values{Home: home}}
	bridged := mcpRuntime(runtime, false)

	write(`{"version":2,"claude":{"systemPrompt":"verbose"}}`)
	var stderr strings.Builder
	code := bridged.Dispatch(context.Background(), []string{"chat"}, io.Discard, &stderr)
	want := `pfm: config: config ` + path + `: claude.systemPrompt must be "production", "lean" or "professor", got "verbose"` + "\n"
	if code != 1 || stderr.String() != want {
		t.Fatalf("Dispatch after a broken edit = %d %q, want 1 %q", code, stderr.String(), want)
	}
}
