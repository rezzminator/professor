package mockengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexappendix"
)

func TestReplaceCodexHookStatePreservesFollowingOwnershipComments(t *testing.T) {
	for _, following := range []string{
		"# BEGIN fixture server\n[mcp_servers.fixture]\ncommand = 'fixture'\n# END fixture server\n",
		"# trailing installer ownership\n",
	} {
		raw := "[hooks.state.fixture]\nenabled = false\ntrusted_hash = 'old'\n\n" + following
		updated, err := replaceCodexHookState(raw, "fixture", map[string]any{
			"enabled": true, "trusted_hash": "new",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(updated, following) {
			t.Fatalf("hook trust replacement removed following ownership bytes: %s", updated)
		}
	}
}

func TestCodexAppServerRegistersAndReadsNativeHookTrust(t *testing.T) {
	fix := newFixture(t)
	setScenarioField(t, fix, "rate_limits", json.RawMessage(fixtureRateLimits))
	path := filepath.Join(fix.codexHome, "hooks.json")
	hooks := `{"hooks":{"SessionStart":[{"matcher":"resume","hooks":[{"type":"command","command":"printf fixture"}]}]}}`
	if err := os.WriteFile(path, []byte(hooks), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(fix.codexHome, "config.toml")
	original := "# keep installer ownership\nmodel = 'fixture-model'\n" +
		"# BEGIN fixture prompt\ndeveloper_instructions = '''Fleet rules.\n[not.a.table]\n'''\n# END fixture prompt\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binary := filepath.Join(fix.bin, "codex")
	handler := codexappendix.Handler{Event: "sessionStart", Matcher: "resume", Command: "printf fixture"}
	if err := codexappendix.RegisterHookTrust(
		ctx,
		binary,
		fix.codexHome,
		"sessionStart",
		"resume",
		"printf fixture",
	); err != nil {
		t.Fatal(err)
	}
	trusted, err := codexappendix.NativeHookTrustState(ctx, binary, fix.codexHome, handler)
	if err != nil || !trusted {
		t.Fatalf("native readback trusted=%v err=%v", trusted, err)
	}
	config, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(config), original) {
		t.Fatalf("native trust write changed unrelated config bytes: %s err=%v", config, err)
	}
	// Same command and key, different handler semantics: the old hash cannot trust it.
	changed := strings.Replace(hooks, `"command":"printf fixture"`, `"command":"printf fixture","timeout":2`, 1)
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if trusted, err = codexappendix.NativeHookTrustState(
		ctx,
		binary,
		fix.codexHome,
		handler,
	); err == nil ||
		trusted {
		t.Fatalf("changed handler trusted=%v err=%v", trusted, err)
	}
	// Replacing an existing trust entry must preserve unrelated text too.
	if err := codexappendix.RegisterHookTrust(
		ctx, binary, fix.codexHome, "sessionStart", "resume", "printf fixture",
	); err != nil {
		t.Fatal(err)
	}
	if trusted, err = codexappendix.NativeHookTrustState(ctx, binary, fix.codexHome, handler); err != nil || !trusted {
		t.Fatalf("updated native readback trusted=%v err=%v", trusted, err)
	}
	config, err = os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(config), original) {
		t.Fatalf("native trust replacement changed unrelated config bytes: %s err=%v", config, err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if trusted, err = codexappendix.NativeHookTrustState(
		ctx,
		binary,
		fix.codexHome,
		handler,
	); err == nil ||
		trusted {
		t.Fatalf("malformed hook source trusted=%v err=%v", trusted, err)
	}
}
