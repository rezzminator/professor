package hostcheck

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

func TestStrayDir(t *testing.T) {
	env := fixtureEnv(t)
	root := filepath.Dir(config.DefaultAccountDir(env.Home, 1))
	empty, filled, file := filepath.Join(root, ".git"), filepath.Join(root, ".codex"), filepath.Join(root, ".agents")
	makeDir(t, empty)
	writeFile(t, filepath.Join(filled, "keep"), "data")
	writeFile(t, file, "data")
	assertRows(t, detect(t, "stray-dir", env),
		Row{Warn, "stray-dir", empty, "empty stray dir", "rmdir " + empty},
		Row{Warn, "stray-dir", filled, "stray dir holds 1 entries", "inspect, then rm -r " + filled},
		Row{Warn, "stray-dir", file, "not a directory", "inspect, then rm -r " + file})
	t.Run("unreadable", func(t *testing.T) {
		env := fixtureEnv(t)
		root := filepath.Dir(config.DefaultAccountDir(env.Home, 1))
		writeFile(t, root, "file")
		rows := detect(t, "stray-dir", env)
		if len(rows) != 3 {
			t.Fatalf("rows=%v", rows)
		}
		for i, name := range []string{".git", ".codex", ".agents"} {
			assertUnreadable(t, rows[i:i+1], "stray-dir", filepath.Join(root, name), syscall.ENOTDIR)
		}
	})
}
