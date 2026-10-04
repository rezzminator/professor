package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// RetiredStoreEntries names every file once linked from every account and now
// per-account, each also in AccountEntries. Claude's native updater writes
// .last-update-result.json by temp file and rename at the account path, which
// replaces a link with a real file. A future retirement is one more row here.
var RetiredStoreEntries = []string{".last-update-result.json"}

// RetiredStoreArchive is the directory install moves a retired entry's store
// copy into.
func RetiredStoreArchive(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "retired-store-entries")
}

// RetiredStoreLink reports whether path is a link resolving to the store's
// copy of its entry; base resolves a relative target.
func RetiredStoreLink(store, base, path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect retired store entry %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return false, fmt.Errorf("read retired store link %s: %w", path, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	return paths.PhysicalPath(target) == paths.PhysicalPath(filepath.Join(store, filepath.Base(path))), nil
}

// retireStoreEntries migrates every retired entry: each account's link into
// the store goes first, so no account is left dangling, then the store copy
// moves into the archive. A real account file is the account's own and stays.
func (installer *engine) retireStoreEntries() error {
	store := installer.options.ConfigDir
	report := InspectClaudeStore(store, installer.options.ClaudeAccounts)
	for _, name := range RetiredStoreEntries {
		keep := ""
		for _, account := range report.Accounts {
			switch account.State {
			case stateStore:
				keep = fmt.Sprintf("account %d %s resolves to the store", account.ID, account.Dir)
				continue
			case stateUnreadable:
				return fmt.Errorf("inspect account %d %s: %w", account.ID, account.Dir, account.Err)
			case string(HostOverlayMissing):
				continue
			}
			path := filepath.Join(account.Dir, name)
			linked, err := RetiredStoreLink(store, paths.PhysicalPath(account.Dir), path)
			if err != nil {
				return err
			}
			if !linked {
				continue
			}
			if err := installer.change("unlink "+path+" (a per-account file now)", func() error {
				return os.Remove(path)
			}); err != nil {
				return fmt.Errorf("unlink retired store entry %s: %w", path, err)
			}
		}
		if err := installer.archiveRetiredStoreEntry(filepath.Join(store, name), keep); err != nil {
			return err
		}
	}
	return nil
}

// archiveRetiredStoreEntry moves a retired entry's store copy into the
// archive, stamped; it never deletes it.
func (installer *engine) archiveRetiredStoreEntry(path, keep string) error {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect retired store entry %s: %w", path, err)
	}
	if keep != "" {
		installer.skip("keep " + path + ": " + keep + " — pfm doctor names the fix")
		return nil
	}
	archiveDir := RetiredStoreArchive(installer.options.Home)
	archive := availableBackup(filepath.Join(archiveDir, filepath.Base(path)), installer.stamp)
	return installer.change("archive "+path+" -> "+archive, func() error {
		if err := os.MkdirAll(archiveDir, 0o700); err != nil {
			return fmt.Errorf("archive retired store entry %s: create %s: %w", path, archiveDir, err)
		}
		if err := os.Rename(path, archive); err != nil {
			return fmt.Errorf("archive retired store entry %s -> %s: %w", path, archive, err)
		}
		return nil
	})
}
