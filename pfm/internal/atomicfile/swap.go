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
func SymlinkOverDir(target, source string) (err error) {
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
	if err := swapPaths(staged, target); err != nil {
		if removeErr := os.Remove(staged); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove staged global link %s: %w", staged, removeErr))
		}
		return fmt.Errorf("swap global link %s -> %s: %w", target, source, err)
	}
	if err := os.RemoveAll(staged); err != nil {
		return fmt.Errorf("remove displaced directory %s: %w", staged, err)
	}
	return nil
}
