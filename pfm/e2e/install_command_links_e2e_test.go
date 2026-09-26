//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
)

var commandRoots = []string{
	e2eCommandRoot,
	".cc/1/commands",
	".cc/2/commands",
	".cc/3/commands",
}

func (h *e2eHarness) assertCommandLinksInstalled(home string) {
	h.t.Helper()
	for _, root := range commandRoots {
		for _, relative := range commandLinks {
			path := filepath.Join(home, root, relative)
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				h.t.Fatalf(
					"install surface failed; differing paths: %s; status: %v",
					filepath.Join(root, relative),
					err,
				)
			}
			if _, err := filepath.EvalSymlinks(path); err != nil {
				h.t.Fatalf(
					"install surface failed; differing paths: %s unresolved symlink; status: %v",
					filepath.Join(root, relative),
					err,
				)
			}
		}
	}
}

func (h *e2eHarness) assertCommandLinksUninstalled(home string) {
	h.t.Helper()
	for _, root := range commandRoots {
		for _, relative := range commandLinks {
			path := filepath.Join(root, relative)
			if _, err := os.Lstat(filepath.Join(home, path)); !os.IsNotExist(err) {
				h.t.Fatalf("uninstall failed; differing paths: %s; status: %v", path, err)
			}
		}
	}
}
