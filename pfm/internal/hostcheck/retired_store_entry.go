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
// An account with a live chat, which writes the entry, holds install's
// migration back by design (installer.LiveChatDeferral, the door install
// asks): its link row, and the store copy's, name that reason as deferred.
func retiredStoreEntry(env Env) ([]Row, error) {
	var rows []Row
	deferred := map[string]string{}
	held := ""
	for _, account := range sortedAccounts(env) {
		if claudelaunch.InspectConfigDir(env.Store, account.ConfigDir).State == claudelaunch.ConfigDirStore {
			continue
		}
		wait, err := installer.LiveChatDeferral(env.ProcRoot, account.ConfigDir)
		if err != nil {
			wait = "live chats in " + account.ConfigDir + " unreadable: " + err.Error()
		}
		deferred[account.ConfigDir] = wait
		if held == "" {
			held = wait
		}
	}
	withDeferral := func(problem, wait string) string {
		if wait == "" {
			return problem
		}
		return problem + "; deferred: " + wait
	}
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
					withDeferral(
						name+" links into the store; it is a per-account file now",
						deferred[account.ConfigDir],
					),
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
			withDeferral(name+" is a per-account file now; the store copy is retired", held),
			"run pfm install; it archives the copy under " + installer.RetiredStoreArchive(env.Home),
		})
	}
	return rows, nil
}
