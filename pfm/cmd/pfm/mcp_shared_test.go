package main

import (
	"context"
	"io"
	"strings"
	"testing"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestMCPRuntimeBindsTheVerbLayerToTheCommandsRuntime pins the one bridge into
// MCP: the typed verbs and the roster rung both run on the command's own
// runtime — the same one, never a second load — and the argv dispatcher left
// for the stateful verbs refuses anything but chat argv.
func TestMCPRuntimeBindsTheVerbLayerToTheCommandsRuntime(t *testing.T) {
	bridged := mcpRuntime(commandRuntime{Paths: paths.Values{TmuxDir: "/jail/tmux"}}, false)
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
