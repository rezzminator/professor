package hostcheck

import (
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const checkRetiredStoreEntry = "retired-store-entry"

// retiredStoreEntry names what a retired store entry leaves behind: an account
// link into the store and the store copy. Each is a warning, never a block,
// since pfm install, which a block would refuse, is the step that migrates it.
func retiredStoreEntry(env Env) ([]Row, error) {
	var rows []Row
	for _, name := range installer.RetiredStoreEntries {
		for _, account := range sortedAccounts(env) {
			if claudelaunch.InspectConfigDir(env.Store, account.ConfigDir).State == claudelaunch.ConfigDirStore {
				continue
			}
			path := filepath.Join(account.ConfigDir, name)
			linked, err := installer.RetiredStoreLink(env.Store, paths.PhysicalPath(account.ConfigDir), path)
			if err != nil {
				rows = append(rows, unreadable(checkRetiredStoreEntry, path, err))
				continue
			}
			if linked {
				rows = append(rows, Row{
					Warn,
					checkRetiredStoreEntry,
					path,
					name + " links into the store; it is a per-account file now",
					"run pfm install; it removes the link, and Claude writes this account's own file",
				})
			}
		}
		path := filepath.Join(env.Store, name)
		if inspectPath(&rows, checkRetiredStoreEntry, path) == nil {
			continue
		}
		rows = append(rows, Row{
			Warn,
			checkRetiredStoreEntry,
			path,
			name + " is a per-account file now; the store copy is retired",
			"run pfm install; it archives the copy under " + installer.RetiredStoreArchive(env.Home),
		})
	}
	return rows, nil
}
