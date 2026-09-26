//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The jailed native CLI implements only the hook discovery/trust protocol used
// by installation. Re-entering this test binary keeps the fixture portable and
// avoids requiring a real Codex account or model request in installer e2e.
func TestCodexHookAPIFixture(t *testing.T) {
	if os.Getenv("PFM_E2E_CODEX_HOOK_FIXTURE") != "1" {
		t.Skip("needs PFM_E2E_CODEX_HOOK_FIXTURE=1")
	}
	account := os.Getenv("CODEX_HOME")
	source := filepath.Join(account, "hooks.json")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	hooks := []map[string]any{}
	for _, group := range doc.Hooks["SessionStart"] {
		for _, hook := range group.Hooks {
			sum := sha256.Sum256([]byte(hook.Command))
			hooks = append(
				hooks,
				map[string]any{
					"key":         "fixture-hook",
					"command":     hook.Command,
					"sourcePath":  source,
					"source":      "user",
					"currentHash": hex.EncodeToString(sum[:]),
					"eventName":   "sessionStart",
					"enabled":     true,
					"trustStatus": "untrusted",
				},
			)
		}
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := decoder.Decode(&request); err == io.EOF {
			return
		} else if err != nil {
			t.Fatal(err)
		}
		if request.ID == nil {
			continue
		}
		result := any(map[string]any{})
		switch request.Method {
		case "initialize":
		case "hooks/list":
			result = map[string]any{"data": []any{map[string]any{"hooks": hooks}}}
		case "config/value/write":
			var params struct {
				KeyPath string `json:"keyPath"`
				Value   struct {
					Enabled bool   `json:"enabled"`
					Hash    string `json:"trusted_hash"`
				} `json:"value"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			if len(hooks) != 1 || params.KeyPath != `hooks.state."fixture-hook"` || !params.Value.Enabled ||
				params.Value.Hash != hooks[0]["currentHash"] {
				t.Fatalf("unexpected trust request: %s", request.Params)
			}
			if err := os.WriteFile(
				filepath.Join(account, "fixture-hook-trust.json"),
				request.Params,
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unsupported fixture API %s", request.Method)
		}
		if err := encoder.Encode(map[string]any{"id": *request.ID, "result": result}); err != nil {
			t.Fatal(err)
		}
	}
}
