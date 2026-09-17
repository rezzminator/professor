package kill

import (
	pfmengine "hostops/pfm/internal/engine"
	claudeengine "hostops/pfm/internal/engine/claude"
	codexengine "hostops/pfm/internal/engine/codex"
	opencodeengine "hostops/pfm/internal/engine/opencode"
	"hostops/pfm/internal/index"
)

// The composition root (cmd/pfm/engines.go) wires the engines in production;
// kill's tests index real transcripts, so they wire every engine's source — an
// index pass refuses an engine it cannot index.
func init() {
	index.RegisterSource(pfmengine.Claude, claudeengine.Source{})
	index.RegisterSource(pfmengine.Codex, codexengine.Source{})
	index.RegisterSource(pfmengine.OpenCode, opencodeengine.Source{})
}
