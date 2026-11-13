//go:build jail

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
	trustedConfig := "# keep installer ownership\nmodel = 'fixture-model'\n# BEGIN fixture prompt\ndeveloper_instructions = '''Fleet rules.\n[not.a.table]\n'''\n# END fixture prompt\n[hooks.state.\"{root}/codex/hooks.json:session_start:0:0\"]\nenabled = true\ntrusted_hash = \"sha256:420caf7789a41a86ac4c3a57193100780bce5b3d11b5226f3af07b6ee378ac59\"\n"
	if got := strings.ReplaceAll(readFile(t, configPath), fix.root, "{root}"); got != trustedConfig {
		t.Fatalf("native trust write = %q, want %q", got, trustedConfig)
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
	replaced := strings.Replace(trustedConfig, "420caf7789a41a86ac4c3a57193100780bce5b3d11b5226f3af07b6ee378ac59",
		"b156722ee42e0b61996f67e48169c112a69dafe87513b85454e3dc01a91e9201", 1)
	if got := strings.ReplaceAll(readFile(t, configPath), fix.root, "{root}"); got != replaced {
		t.Fatalf("native trust replacement = %q, want %q", got, replaced)
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
