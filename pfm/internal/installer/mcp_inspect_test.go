package installer

import (
	"path/filepath"
	"testing"
)

// A standalone harvester entry in the Claude user registry (~/.claude.json)
// is the legacy state the cutover exists to find; reading only the Codex side
// or a project .mcp.json would report this machine migrated when it is not.
func TestInspectHarvesterClientCutoverFlagsAStandaloneEntryInTheClaudeUserRegistry(t *testing.T) {
	home := t.TempDir()
	writeFixture(
		t,
		filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"harvester":{"type":"stdio","command":"uv","args":["run","harvester"]}}}`,
	)
	registries := []string{filepath.Join(home, ".claude.json")}
	for _, report := range InspectHarvesterClientCutover(home, 8377, registries, nil) {
		if report.State == MCPClientLegacyStandalone {
			return
		}
	}
	t.Fatal("the Claude user registry's standalone harvester was reported migrated")
}

// TestInspectHarvesterClientCutoverRefusesANilRegistryList pins issue #24
// finding 5's part D: a nil registries argument is a programming error, not
// "use the historical $HOME/.claude.json default" — the caller must resolve
// the actual roster (installer.ClaudeUserRegistries). Silently falling back
// to one hardcoded path is exactly how doctor and the writer disagreed with
// no indication why; refusing loudly with one MCPClientUnreadable report
// naming the missing list is the fix.
func TestInspectHarvesterClientCutoverRefusesANilRegistryList(t *testing.T) {
	home := t.TempDir()
	reports := InspectHarvesterClientCutover(home, 8377, nil, nil)
	if len(reports) != 1 {
		t.Fatalf("reports=%#v, want exactly one unreadable report naming the missing list", reports)
	}
	if reports[0].State != MCPClientUnreadable {
		t.Fatalf("reports[0].State=%q, want %q", reports[0].State, MCPClientUnreadable)
	}
	if reports[0].Error == nil || reports[0].Error.Error() != "no Claude registries supplied" {
		t.Fatalf("reports[0].Error=%v, want an error naming the missing registry list", reports[0].Error)
	}
}
