package hostfixture

import (
	"os"
	"path/filepath"
	"testing"
)

// ConfigDirFixture is SymlinkedConfigDir's result: the jailed Base plus the
// symlink and the physical directory it resolves to.
type ConfigDirFixture struct {
	Base
	ConfigDir string // the ~/.claude path — a symlink, not a real directory
	Physical  string // the real directory ConfigDir points at
}

// SymlinkedConfigDir jails a fleet whose ~/.claude is a symlink to a
// physical directory elsewhere in the jail — the state
// installer.claudeConfigDirs, paths.DevRepoGitDir and professor.storeSHA
// must resolve THROUGH (os.Stat, not os.Lstat, follows it) rather than
// silently treat as though .claude were the physical directory itself.
func SymlinkedConfigDir(t *testing.T) ConfigDirFixture {
	t.Helper()
	base := newBase(t)
	physical := filepath.Join(base.Root, "claude-physical")
	if err := os.MkdirAll(physical, 0o700); err != nil {
		t.Fatalf("hostfixture: create physical config dir: %v", err)
	}
	link := filepath.Join(base.Values.Home, ".claude")
	if err := os.Symlink(physical, link); err != nil {
		t.Fatalf("hostfixture: symlink .claude to the physical dir: %v", err)
	}
	return ConfigDirFixture{Base: base, ConfigDir: link, Physical: physical}
}
