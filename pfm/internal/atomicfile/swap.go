package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SymlinkOverDir replaces the real directory at target with a link to source
// without target ever being absent: the link is staged beside target, then
// exchanged with the directory in one atomic swap (renameat2 RENAME_EXCHANGE
// on Linux, renamex_np RENAME_SWAP on macOS), and the displaced directory,
// now at the staged name, is removed last. A platform without the swap, or a
// filesystem refusing it, returns an error and leaves target untouched.
func SymlinkOverDir(target, source string, validate ...func(string) error) error {
	return replaceDirectoryWithSymlink(target, source, nil, validate...)
}

// beforeRollback is a per-call test seam at the last identity-check/swap boundary.
func replaceDirectoryWithSymlink(
	target, source string,
	beforeRollback func(),
	validate ...func(string) error,
) (err error) {
	staged, err := os.MkdirTemp(filepath.Dir(target), LinkStagePrefix+"*")
	if err != nil {
		return fmt.Errorf("stage global link %s: %w", target, err)
	}
	if err := os.Remove(staged); err != nil {
		return fmt.Errorf("prepare global link %s: %w", target, err)
	}
	if err := os.Symlink(source, staged); err != nil {
		return fmt.Errorf("stage global link %s -> %s: %w", staged, source, err)
	}
	stagedInfo, err := os.Lstat(staged)
	if err != nil {
		return fmt.Errorf("identify staged global link %s: %w", staged, err)
	}
	if err := swapPaths(staged, target); err != nil {
		if removeErr := os.Remove(staged); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove staged global link %s: %w", staged, removeErr))
		}
		return fmt.Errorf("swap global link %s -> %s: %w", target, source, err)
	}
	for _, check := range validate {
		validationErr := check(staged)
		if validationErr == nil {
			continue
		}
		conflict := fmt.Errorf("validate displaced entry %s (preserved at %s): %w", target, staged, validationErr)
		if err := proveStagedSymlink(target, source, stagedInfo); err != nil {
			return errors.Join(conflict, fmt.Errorf("refuse rollback: %w", err))
		}
		if beforeRollback != nil {
			beforeRollback()
		}
		// The first proof cannot authorize a later swap: a replacement at this
		// boundary remains at target, while the displaced directory stays staged.
		if err := proveStagedSymlink(target, source, stagedInfo); err != nil {
			return errors.Join(conflict, fmt.Errorf("refuse raced rollback: %w", err))
		}
		if err := swapPaths(staged, target); err != nil {
			return errors.Join(conflict, fmt.Errorf("rollback global link %s: %w", target, err))
		}
		if err := proveStagedSymlink(staged, source, stagedInfo); err != nil {
			return errors.Join(conflict, fmt.Errorf("preserve raced rollback entry: %w", err))
		}
		if err := os.Remove(staged); err != nil {
			return errors.Join(conflict, fmt.Errorf("remove rejected staged link %s: %w", staged, err))
		}
		return fmt.Errorf("preserved changed entry %s: %w", target, validationErr)
	}
	if err := os.RemoveAll(staged); err != nil {
		return fmt.Errorf("remove displaced directory %s: %w", staged, err)
	}
	return nil
}

func proveStagedSymlink(path, source string, stagedInfo fs.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("identify link %s: %w", path, err)
	}
	if current.Mode()&os.ModeSymlink == 0 || !os.SameFile(stagedInfo, current) {
		return fmt.Errorf("link %s changed identity or type", path)
	}
	link, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("read link %s: %w", path, err)
	}
	if link != source {
		return fmt.Errorf("link %s changed target", path)
	}
	return nil
}
