package testjail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// PinClaudeAsk records the user's choice of Claude for asks, so a Claude-only
// fixture reads as a clean doctor home rather than an ask.engine fallback.
func PinClaudeAsk(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, "pfm.config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pin ask.engine in %s: %v", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("pin ask.engine in %s: %v", path, err)
	}
	ask := make(map[string]any)
	if value, exists := document["ask"]; exists {
		var ok bool
		ask, ok = value.(map[string]any)
		if !ok {
			t.Fatalf("pin ask.engine in %s: %v", path, fmt.Errorf("ask is %T, not an object", value))
		}
	}
	ask["engine"] = pfmengine.MustLookup(pfmengine.Claude).LongName
	document["ask"] = ask
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("pin ask.engine in %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o600); err != nil {
		t.Fatalf("pin ask.engine in %s: %v", path, err)
	}
}
