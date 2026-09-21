package mcpserv

import (
	"context"
	"io"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestChatRuntimeIdentityCanonicalInputs(t *testing.T) {
	base := Runtime{
		Paths: paths.Values{Home: "/home/a", DB: "/db", Roots: map[pfmengine.ID][]string{
			pfmengine.Codex: {"/c"}, pfmengine.Claude: {"/a", "/b"},
		}},
		Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: "/account"}}, ConfigPath: "/config",
		ClaudeBinary: "claude-a", CodexBinary: "codex-a", OpenCodeBinary: "opencode-a",
	}
	identity, err := deriveChatRuntimeIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	equivalent := base
	equivalent.Paths.Roots = map[pfmengine.ID][]string{
		pfmengine.Claude: {"/a", "/b"}, pfmengine.Codex: {"/c"},
	}
	equivalent.Clock = clock.Real
	equivalent.DaemonAddress = "127.0.0.1:9999"
	equivalent.AllowAmbientIdentity = true
	equivalent.Dispatch = func(context.Context, []string, io.Writer, io.Writer) int { return 0 }
	got, err := deriveChatRuntimeIdentity(equivalent)
	if err != nil {
		t.Fatal(err)
	}
	if got != identity {
		t.Fatalf("irrelevant fields changed identity: %q != %q", got, identity)
	}
	changed := base
	changed.Accounts = []pfmconfig.Account{{ID: 2, ConfigDir: "/account"}}
	other, err := deriveChatRuntimeIdentity(changed)
	if err != nil {
		t.Fatal(err)
	}
	if other == identity {
		t.Fatal("account policy change did not change identity")
	}
	changed = base
	changed.Paths.Roots = map[pfmengine.ID][]string{pfmengine.Claude: {"/b", "/a"}, pfmengine.Codex: {"/c"}}
	other, _ = deriveChatRuntimeIdentity(changed)
	if other == identity {
		t.Fatal("root precedence change did not change identity")
	}
}
