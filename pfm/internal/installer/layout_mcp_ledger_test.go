package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLayoutApplyStripsAccountMCPLedgerKeyedThroughLink proves a ledger key
// written as the path was reached (macOS /tmp is a link to /private/tmp) still
// owns the registry it resolves to: classify reports the strip, apply removes
// the entry and retires the key.
func TestLayoutApplyStripsAccountMCPLedgerKeyedThroughLink(t *testing.T) {
	env := layoutFixture(t)
	account := env.Config.Accounts[1].ConfigDir
	registry := filepath.Join(account, ".claude.json")
	layoutWrite(t, registry, `{"mcpServers":{"chat":{"command":"pfm"},"operator":{"command":"own"}}}`)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(account, alias); err != nil {
		t.Fatal(err)
	}
	reached := filepath.Join(alias, ".claude.json")
	mcpLedger := filepath.Join(env.ManagedRoot, mcpOwnershipName)
	mcpRaw, err := json.Marshal(mcpOwnership{
		Registrations: map[string]map[string]any{reached: {"chat": map[string]any{"command": "pfm"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, mcpLedger, string(mcpRaw))
	var output bytes.Buffer
	if _, err := ApplyLayout(context.Background(), env, nil, true, &output); err != nil {
		t.Fatalf("apply: %v\n%s", err, output.String())
	}
	updated, err := os.ReadFile(registry)
	if err != nil || strings.Contains(string(updated), `"chat"`) || !strings.Contains(string(updated), `"operator"`) {
		t.Fatalf("registry=%s err=%v\n%s", updated, err, output.String())
	}
	after, err := readMCPOwnership(mcpLedger)
	if err != nil || len(after.Registrations[reached]) != 0 {
		t.Fatalf("MCP ledger still owns the linked key: %v err=%v", after, err)
	}
}
