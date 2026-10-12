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
	if err := os.WriteFile(configPath, []byte("model = \"fixture-model\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binary := filepath.Join(fix.bin, "codex")
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
	trusted, err := codexappendix.NativeHookTrustState(ctx, binary, fix.codexHome, "printf fixture")
	if err != nil || !trusted {
		t.Fatalf("native readback trusted=%v err=%v", trusted, err)
	}
	config, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(config), `model = "fixture-model"`) {
		t.Fatalf("unrelated config lost: %s err=%v", config, err)
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
		"printf fixture",
	); err == nil ||
		trusted {
		t.Fatalf("changed handler trusted=%v err=%v", trusted, err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if trusted, err = codexappendix.NativeHookTrustState(
		ctx,
		binary,
		fix.codexHome,
		"printf fixture",
	); err == nil ||
		trusted {
		t.Fatalf("malformed hook source trusted=%v err=%v", trusted, err)
	}
}
