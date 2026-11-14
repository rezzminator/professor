package mockengine

import (
	"testing"
)

func TestReplaceCodexHookStatePreservesFollowingOwnershipComments(t *testing.T) {
	for following, want := range map[string]string{
		"# BEGIN fixture server\n[mcp_servers.fixture]\ncommand = 'fixture'\n# END fixture server\n": "\n# BEGIN fixture server\n[mcp_servers.fixture]\ncommand = 'fixture'\n# END fixture server\n[hooks.state.\"fixture\"]\nenabled = true\ntrusted_hash = \"new\"\n",
		"# trailing installer ownership\n": "\n# trailing installer ownership\n[hooks.state.\"fixture\"]\nenabled = true\ntrusted_hash = \"new\"\n",
	} {
		raw := "[hooks.state.fixture]\nenabled = false\ntrusted_hash = 'old'\n\n" + following
		updated, err := replaceCodexHookState(raw, "fixture", map[string]any{
			"enabled": true, "trusted_hash": "new",
		})
		if err != nil {
			t.Fatal(err)
		}
		if updated != want {
			t.Fatalf("hook trust replacement = %q, want %q", updated, want)
		}
	}
}
