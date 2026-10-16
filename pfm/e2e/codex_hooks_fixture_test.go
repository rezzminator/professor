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
	"sort"
	"strings"
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
	events := make([]string, 0, len(doc.Hooks))
	for event := range doc.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	for _, event := range events {
		for groupIndex, group := range doc.Hooks[event] {
			for hookIndex, hook := range group.Hooks {
				sum := sha256.Sum256([]byte(event + "\x00" + hook.Command))
				var matcher any
				if group.Matcher != "" {
					matcher = group.Matcher
				}
				hooks = append(
					hooks,
					map[string]any{
						"key": fmt.Sprintf(
							"%s:%s:%d:%d",
							source,
							fixtureSnakeEvent(event),
							groupIndex,
							hookIndex,
						),
						"command":     hook.Command,
						"handlerType": "command",
						"matcher":     matcher,
						"sourcePath":  source,
						"source":      "user",
						"currentHash": hex.EncodeToString(sum[:]),
						"eventName":   strings.ToLower(event[:1]) + event[1:],
						"enabled":     true,
						"trustStatus": "untrusted",
					},
				)
			}
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
			trusts := readFixtureTrusts(t, account)
			for _, hook := range hooks {
				if trust, written := trusts["hooks.state."+quoteHookKey(t, hook["key"].(string))]; written {
					hook["enabled"] = trust.Value.Enabled
					if trust.Value.Hash == hook["currentHash"] {
						hook["trustStatus"] = "trusted"
					}
				}
			}
			result = map[string]any{"data": []any{map[string]any{"hooks": hooks}}}
		case "config/value/write":
			var params fixtureHookTrust
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			// The request must name one listed handler by its key and hash;
			// pfm's SessionStart group sits beside the Herdr reporter's, and
			// every owned handler is trusted by its own request.
			matched := 0
			for _, hook := range hooks {
				if params.KeyPath == "hooks.state."+quoteHookKey(t, hook["key"].(string)) &&
					params.Value.Hash == hook["currentHash"] {
					matched++
				}
			}
			if matched != 1 || !params.Value.Enabled {
				t.Fatalf("unexpected trust request: %s", request.Params)
			}
			trusts := readFixtureTrusts(t, account)
			trusts[params.KeyPath] = params
			encoded, err := json.Marshal(trusts)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(account, "fixture-hook-trust.json"), encoded, 0o600); err != nil {
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

// readFixtureTrusts is every trust the fixture was asked to write, by key path.
func readFixtureTrusts(t *testing.T, account string) map[string]fixtureHookTrust {
	t.Helper()
	trusts := map[string]fixtureHookTrust{}
	state, err := os.ReadFile(filepath.Join(account, "fixture-hook-trust.json"))
	if errors.Is(err, os.ErrNotExist) {
		return trusts
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(state, &trusts); err != nil {
		t.Fatal(err)
	}
	return trusts
}

// fixtureSnakeEvent spells an event as a native hook key does ("PostToolUse" ->
// "post_tool_use").
func fixtureSnakeEvent(event string) string {
	var out strings.Builder
	for index, r := range event {
		if r >= 'A' && r <= 'Z' {
			if index > 0 {
				out.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		out.WriteRune(r)
	}
	return out.String()
}
