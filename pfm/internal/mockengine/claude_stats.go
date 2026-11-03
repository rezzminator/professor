package mockengine

import (
	_ "embed"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

//go:embed testdata/stats-cache.json
var statsCache []byte

func (session *claudeSession) stats() error {
	return atomicfile.Write(filepath.Join(session.configDir, "stats-cache.json"), statsCache, 0o600)
}
