package installer

import "path/filepath"

// retireStateDirs removes whole each directory under ~/.local/state/pfm the
// retired lists name (stateDirs): pfm wrote it and its writer is gone. A dry
// run names what it would free, a failed removal is a warn line naming the path.
func (installer *engine) retireStateDirs() {
	// A failed read is deferred once by Run; no directory is removed without the list.
	lists, _ := installer.retiredLists()
	for _, dir := range lists.StateDirs {
		path := filepath.Join(installer.options.Home, ".local", "state", "pfm", dir.Name)
		installer.removeStatePath(path, dir.What, "")
	}
}
