package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/rezzminator/professor/pfm/internal/retired"
)

// ManagedRoot is the install ownership ledger directory.
func ManagedRoot(home string) string { return managedRootForHome(home) }

// PFMSettingsLeftovers reports only settings owned by pfm, through links; a
// hook the retired lists name is pfm's too.
func PFMSettingsLeftovers(lists retired.Lists, home, path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	}
	ownershipPath := settingsHookOwnershipPath(ManagedRoot(home))
	ownership, _, ledgerErr := readSettingsHookOwnership(ownershipPath)
	if ledgerErr != nil {
		return nil, probePathError(ownershipPath, ledgerErr)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	names, err := accountSettingsLeftovers(lists, raw, home, ownership[physicalSettingsPath(path)], true)
	if err != nil {
		return nil, probePathError(path, err)
	}
	return names, nil
}

func probePathError(path string, err error) error {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		err = &fs.PathError{Op: "read", Path: path, Err: err}
	}
	return fmt.Errorf("probe %s: %w", path, err)
}
