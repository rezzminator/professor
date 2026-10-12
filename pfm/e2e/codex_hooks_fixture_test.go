//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type fixtureHookTrust struct {
	KeyPath string `json:"keyPath"`
	Value   struct {
		Enabled bool   `json:"enabled"`
		Hash    string `json:"trusted_hash"`
	} `json:"value"`
}

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
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	hooks := []map[string]any{}
	for groupIndex, group := range doc.Hooks["SessionStart"] {
		for hookIndex, hook := range group.Hooks {
			sum := sha256.Sum256([]byte(hook.Command))
			hooks = append(
				hooks,
				map[string]any{
					"key":         fmt.Sprintf("%s:session_start:%d:%d", source, groupIndex, hookIndex),
					"command":     hook.Command,
					"handlerType": "command",
					"matcher":     group.Matcher,
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
			var trust fixtureHookTrust
			state, err := os.ReadFile(filepath.Join(account, "fixture-hook-trust.json"))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if err == nil {
				if err := json.Unmarshal(state, &trust); err != nil {
					t.Fatal(err)
				}
				for _, hook := range hooks {
					if trust.KeyPath == "hooks.state."+quoteHookKey(t, hook["key"].(string)) {
						hook["enabled"] = trust.Value.Enabled
						if trust.Value.Hash == hook["currentHash"] {
							hook["trustStatus"] = "trusted"
						}
					}
				}
			}
			result = map[string]any{"data": []any{map[string]any{"hooks": hooks}}}
		case "config/value/write":
			var params fixtureHookTrust
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			if len(hooks) != 1 || params.KeyPath != "hooks.state."+quoteHookKey(t, hooks[0]["key"].(string)) ||
				!params.Value.Enabled ||
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

func quoteHookKey(t *testing.T, key string) string {
	t.Helper()
	raw, err := json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
