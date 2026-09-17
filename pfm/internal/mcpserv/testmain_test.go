package mcpserv

import (
	"os"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	claudeengine "hostops/pfm/internal/engine/claude"
	codexengine "hostops/pfm/internal/engine/codex"
	opencodeengine "hostops/pfm/internal/engine/opencode"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/testjail"
)

// TestMain wires the engines the real verb layer scans — the composition root
// (cmd/pfm/engines.go) does it in production, and a scan refuses an engine it
// cannot index — then gives this package a short, canonical TMPDIR before any
// test builds a path from it. See internal/testjail for why both matter.
func TestMain(m *testing.M) {
	index.RegisterSource(pfmengine.Claude, claudeengine.Source{})
	index.RegisterSource(pfmengine.Codex, codexengine.Source{})
	index.RegisterSource(pfmengine.OpenCode, opencodeengine.Source{})
	gather.RegisterMatcher(pfmengine.Claude, claudeengine.Matcher{})
	gather.RegisterMatcher(pfmengine.Codex, codexengine.Matcher{})
	gather.RegisterMatcher(pfmengine.OpenCode, opencodeengine.Matcher{})
	os.Exit(testjail.Run(m))
}
