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

func TestCodexPluginHooksTrustOnlyManagedExtension(t *testing.T) {
	fix := newFixture(t)
	setScenarioField(t, fix, "rate_limits", json.RawMessage(fixtureRateLimits))
	configPath := filepath.Join(fix.codexHome, "config.toml")
	original := "[plugins.\"ponytail@ponytail\"]\nenabled=true\n[plugins.\"foreign@foreign\"]\nenabled=true\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ponytail", "foreign"} {
		path := filepath.Join(
			fix.codexHome,
			"plugins",
			"cache",
			name,
			name,
			"5.3.2",
			"hooks",
			"claude-codex-hooks.json",
		)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			path,
			[]byte(
				`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"printf fixture"}]}],"SubagentStart":[{"hooks":[{"type":"command","command":"printf child"}]}]}}`,
			),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binary := filepath.Join(fix.bin, "codex")
	if err := codexappendix.PluginHookTrustState(ctx, binary, fix.codexHome, "ponytail@ponytail"); err == nil {
		t.Fatal("new hooks appear trusted")
	}
	if err := codexappendix.RegisterPluginHookTrusts(ctx, binary, fix.codexHome, "ponytail@ponytail"); err != nil {
		t.Fatal(err)
	}
	if err := codexappendix.PluginHookTrustState(ctx, binary, fix.codexHome, "ponytail@ponytail"); err != nil {
		t.Fatal(err)
	}
	if err := codexappendix.PluginHookTrustState(ctx, binary, fix.codexHome, "foreign@foreign"); err == nil {
		t.Fatal("another plugin's hooks became trusted")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ponytail@ponytail:hooks/claude-codex-hooks.json:subagent_start:0:0") ||
		strings.Contains(string(raw), "foreign@foreign:hooks/") {
		t.Fatalf("wrong hook namespaces trusted: %s", raw)
	}
	disabled := strings.Replace(string(raw), "enabled=true", "enabled=false", 1)
	if err := os.WriteFile(configPath, []byte(disabled), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := codexappendix.RegisterPluginHookTrusts(ctx, binary, fix.codexHome, "ponytail@ponytail"); err == nil {
		t.Fatal("disabled plugin accepted for trust")
	}
}
