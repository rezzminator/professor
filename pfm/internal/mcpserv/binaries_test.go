package mcpserv

import (
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

// The MCP process hands its configured engine executables to TWO consumers —
// the shared resolver and the inject engine — and they must be the SAME map.
// The resolver was built from a hand-written Claude+Codex literal while
// inject.New (internal/inject/engine.go) builds its own from all three, so an
// MCP process resolved OpenCode panes against a table that did not know the
// engine existed. One map, every registered engine, or a fourth engine lands
// half-configured the same way.
func TestEngineBinariesCoversEveryRegisteredEngine(t *testing.T) {
	runtime := Runtime{
		ClaudeBinary:   "/opt/bin/claude",
		CodexBinary:    "/opt/bin/codex",
		OpenCodeBinary: "/opt/bin/opencode",
	}
	binaries := engineBinaries(runtime)
	for _, id := range pfmengine.All() {
		if binaries[id] == "" {
			t.Fatalf("engineBinaries has no entry for %s: %#v", id, binaries)
		}
	}
	if len(binaries) != len(pfmengine.All()) {
		t.Fatalf("engineBinaries = %#v, want one entry per registered engine", binaries)
	}
	if binaries[pfmengine.OpenCode] != "/opt/bin/opencode" {
		t.Fatalf("OpenCode binary = %q, want the runtime's configured command", binaries[pfmengine.OpenCode])
	}
}
